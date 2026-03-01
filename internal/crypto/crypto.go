package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"os"
)

// deriveKey produces a deterministic 32-byte AES-256 key from machine-specific
// attributes (hostname + working directory). This prevents casual reading of
// API keys from settings.json without requiring a user-provided passphrase.
func deriveKey() []byte {
	hostname, _ := os.Hostname()
	cwd, _ := os.Getwd()
	seed := fmt.Sprintf("gocognigo:%s:%s", hostname, cwd)
	hash := sha256.Sum256([]byte(seed))
	return hash[:]
}

// Encrypt encrypts plaintext using AES-256-GCM and returns a base64-encoded string.
// Returns empty string for empty input (no encryption needed).
func Encrypt(plaintext string) (string, error) {
	if plaintext == "" {
		return "", nil
	}

	key := deriveKey()
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("cipher error: %w", err)
	}

	aesGCM, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("GCM error: %w", err)
	}

	nonce := make([]byte, aesGCM.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("nonce error: %w", err)
	}

	ciphertext := aesGCM.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

// Decrypt decodes a base64 string and decrypts it using AES-256-GCM.
// Returns empty string for empty input.
func Decrypt(encoded string) (string, error) {
	if encoded == "" {
		return "", nil
	}

	ciphertext, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("base64 decode error: %w", err)
	}

	key := deriveKey()
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("cipher error: %w", err)
	}

	aesGCM, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("GCM error: %w", err)
	}

	nonceSize := aesGCM.NonceSize()
	if len(ciphertext) < nonceSize {
		return "", fmt.Errorf("ciphertext too short")
	}

	nonce, ciphertext := ciphertext[:nonceSize], ciphertext[nonceSize:]
	plaintext, err := aesGCM.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("decrypt error: %w", err)
	}

	return string(plaintext), nil
}
