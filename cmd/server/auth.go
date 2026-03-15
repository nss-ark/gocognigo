package main

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// ========== Firebase Auth Config ==========

type FirebaseConfig struct {
	APIKey            string `json:"apiKey"`
	AuthDomain        string `json:"authDomain"`
	ProjectID         string `json:"projectId"`
	StorageBucket     string `json:"storageBucket"`
	MessagingSenderID string `json:"messagingSenderId"`
	AppID             string `json:"appId"`
}

func getFirebaseConfig() *FirebaseConfig {
	projectID := strings.TrimSpace(os.Getenv("FIREBASE_PROJECT_ID"))
	if projectID == "" {
		return nil // Auth not configured
	}
	return &FirebaseConfig{
		APIKey:            strings.TrimSpace(os.Getenv("FIREBASE_API_KEY")),
		AuthDomain:        strings.TrimSpace(os.Getenv("FIREBASE_AUTH_DOMAIN")),
		ProjectID:         projectID,
		StorageBucket:     strings.TrimSpace(os.Getenv("FIREBASE_STORAGE_BUCKET")),
		MessagingSenderID: strings.TrimSpace(os.Getenv("FIREBASE_MESSAGING_SENDER_ID")),
		AppID:             strings.TrimSpace(os.Getenv("FIREBASE_APP_ID")),
	}
}

func (s *Server) handleAuthConfig(w http.ResponseWriter, r *http.Request) {
	config := getFirebaseConfig()
	if config == nil {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "auth not configured"})
		return
	}
	jsonResp(w, config)
}

// ========== Firebase Token Verification ==========

// googleCerts caches Google's public signing certificates for Firebase tokens.
var googleCerts = &certCache{}

type certCache struct {
	mu      sync.RWMutex
	certs   map[string]*rsa.PublicKey
	expires time.Time
}

// fetchGoogleCerts downloads and caches Google's public certificates.
func fetchGoogleCerts() (map[string]*rsa.PublicKey, error) {
	googleCerts.mu.RLock()
	if time.Now().Before(googleCerts.expires) && len(googleCerts.certs) > 0 {
		certs := googleCerts.certs
		googleCerts.mu.RUnlock()
		return certs, nil
	}
	googleCerts.mu.RUnlock()

	googleCerts.mu.Lock()
	defer googleCerts.mu.Unlock()

	// Double-check after acquiring write lock
	if time.Now().Before(googleCerts.expires) && len(googleCerts.certs) > 0 {
		return googleCerts.certs, nil
	}

	// Use a client with a timeout to prevent indefinite hangs
	certClient := &http.Client{Timeout: 10 * time.Second}
	resp, err := certClient.Get("https://www.googleapis.com/robot/v1/metadata/x509/securetoken@system.gserviceaccount.com")
	if err != nil {
		return nil, fmt.Errorf("failed to fetch Google certs: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read certs response: %w", err)
	}

	var rawCerts map[string]string
	if err := json.Unmarshal(body, &rawCerts); err != nil {
		return nil, fmt.Errorf("failed to parse certs: %w", err)
	}

	certs := make(map[string]*rsa.PublicKey)
	for kid, certPEM := range rawCerts {
		block, _ := pem.Decode([]byte(certPEM))
		if block == nil {
			continue
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			continue
		}
		if rsaKey, ok := cert.PublicKey.(*rsa.PublicKey); ok {
			certs[kid] = rsaKey
		}
	}

	// Cache for 1 hour (Google rotates keys roughly every 6 hours)
	googleCerts.certs = certs
	googleCerts.expires = time.Now().Add(1 * time.Hour)

	log.Printf("Refreshed Google signing certs: %d keys cached", len(certs))
	return certs, nil
}

// firebaseToken represents the claims in a Firebase ID token.
type firebaseToken struct {
	Issuer    string `json:"iss"`
	Subject   string `json:"sub"` // This is the user's UID
	Audience  string `json:"aud"`
	IssuedAt  int64  `json:"iat"`
	ExpiresAt int64  `json:"exp"`
	Email     string `json:"email,omitempty"`
	Name      string `json:"name,omitempty"`
}

// verifyFirebaseToken verifies a Firebase ID token and returns the user UID.
func verifyFirebaseToken(idToken string, projectID string) (*firebaseToken, error) {
	// Split JWT parts
	parts := strings.Split(idToken, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("invalid token format")
	}

	// Decode header to get kid
	headerJSON, err := base64URLDecode(parts[0])
	if err != nil {
		return nil, fmt.Errorf("invalid token header: %w", err)
	}
	var header struct {
		Algorithm string `json:"alg"`
		KeyID     string `json:"kid"`
	}
	if err := json.Unmarshal(headerJSON, &header); err != nil {
		return nil, fmt.Errorf("invalid token header JSON: %w", err)
	}
	if header.Algorithm != "RS256" {
		return nil, fmt.Errorf("unsupported algorithm: %s", header.Algorithm)
	}

	// Decode payload
	payloadJSON, err := base64URLDecode(parts[1])
	if err != nil {
		return nil, fmt.Errorf("invalid token payload: %w", err)
	}
	var claims firebaseToken
	if err := json.Unmarshal(payloadJSON, &claims); err != nil {
		return nil, fmt.Errorf("invalid token claims: %w", err)
	}

	// Validate claims
	now := time.Now().Unix()
	if claims.ExpiresAt < now {
		return nil, fmt.Errorf("token expired")
	}
	if claims.IssuedAt > now+300 { // 5 minute clock skew tolerance
		return nil, fmt.Errorf("token issued in the future")
	}
	if claims.Issuer != "https://securetoken.google.com/"+projectID {
		return nil, fmt.Errorf("invalid issuer: %s", claims.Issuer)
	}
	if claims.Audience != projectID {
		return nil, fmt.Errorf("invalid audience: %s", claims.Audience)
	}
	if claims.Subject == "" {
		return nil, fmt.Errorf("empty subject (UID)")
	}

	// Verify signature
	certs, err := fetchGoogleCerts()
	if err != nil {
		return nil, fmt.Errorf("failed to fetch signing keys: %w", err)
	}
	pubKey, ok := certs[header.KeyID]
	if !ok {
		return nil, fmt.Errorf("unknown signing key: %s", header.KeyID)
	}

	// Verify RS256 signature
	signedContent := parts[0] + "." + parts[1]
	signature, err := base64URLDecode(parts[2])
	if err != nil {
		return nil, fmt.Errorf("invalid signature encoding: %w", err)
	}

	hash := crypto.SHA256.New()
	hash.Write([]byte(signedContent))
	hashed := hash.Sum(nil)

	if err := rsa.VerifyPKCS1v15(pubKey, crypto.SHA256, hashed, signature); err != nil {
		return nil, fmt.Errorf("invalid signature: %w", err)
	}

	return &claims, nil
}

func base64URLDecode(s string) ([]byte, error) {
	// Add padding if needed
	switch len(s) % 4 {
	case 2:
		s += "=="
	case 3:
		s += "="
	}
	return base64.URLEncoding.DecodeString(s)
}

// ========== Auth Middleware ==========

type contextKey string

const userUIDKey contextKey = "userUID"
const userEmailKey contextKey = "userEmail"

// authMiddleware checks for a valid Firebase ID token on API requests.
// If FIREBASE_PROJECT_ID is not set, auth is disabled (local dev mode).
func (s *Server) authMiddleware(next http.HandlerFunc) http.HandlerFunc {
	projectID := strings.TrimSpace(os.Getenv("FIREBASE_PROJECT_ID"))

	return func(w http.ResponseWriter, r *http.Request) {
		// Skip auth if not configured (local dev mode)
		if projectID == "" {
			next(w, r)
			return
		}

		path := r.URL.Path

		// Public endpoints that don't require auth
		if path == "/api/auth/config" ||
			path == "/api/community" ||
			path == "/api/community/tags" ||
			!strings.HasPrefix(path, "/api/") {
			next(w, r)
			return
		}

		// Extract token from Authorization header or query param (WebSocket can't send headers)
		token := ""
		authHeader := r.Header.Get("Authorization")
		if strings.HasPrefix(authHeader, "Bearer ") {
			token = strings.TrimPrefix(authHeader, "Bearer ")
		} else if t := r.URL.Query().Get("token"); t != "" {
			token = t
		}
		if token == "" {
			http.Error(w, `{"error":"authentication required"}`, http.StatusUnauthorized)
			return
		}
		claims, err := verifyFirebaseToken(token, projectID)
		if err != nil {
			log.Printf("Auth failed: %v", err)
			http.Error(w, `{"error":"invalid or expired token"}`, http.StatusUnauthorized)
			return
		}

		// Store user UID and email in request context
		ctx := context.WithValue(r.Context(), userUIDKey, claims.Subject)
		ctx = context.WithValue(ctx, userEmailKey, claims.Email)
		next(w, r.WithContext(ctx))
	}
}

// getUserUID extracts the user UID from the request context.
// Returns empty string if auth is not enabled.
func getUserUID(r *http.Request) string {
	uid, _ := r.Context().Value(userUIDKey).(string)
	return uid
}

// getUserEmail extracts the user email from the request context.
// Returns empty string in local mode.
func getUserEmail(r *http.Request) string {
	email, _ := r.Context().Value(userEmailKey).(string)
	return email
}
