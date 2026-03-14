package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// FeedbackEntry stores a single feature request.
type FeedbackEntry struct {
	ID          string    `json:"id"`
	Text        string    `json:"text"`
	UserUID     string    `json:"user_uid,omitempty"`
	UserEmail   string    `json:"user_email,omitempty"`
	GitHubIssue string    `json:"github_issue,omitempty"` // URL of created issue
	CreatedAt   time.Time `json:"created_at"`
}

const feedbackPath = "data/feedback.json"

func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

var feedbackMu sync.Mutex

func loadFeedback() ([]FeedbackEntry, error) {
	data, err := os.ReadFile(feedbackPath)
	if os.IsNotExist(err) {
		return []FeedbackEntry{}, nil
	}
	if err != nil {
		return nil, err
	}
	var entries []FeedbackEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, err
	}
	return entries, nil
}

func saveFeedback(entries []FeedbackEntry) error {
	_ = os.MkdirAll("data", 0755)
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(feedbackPath, data, 0644)
}

// createGitHubIssue opens an issue in the configured repo and returns its HTML URL.
// Returns ("", nil) silently if GITHUB_TOKEN or GITHUB_REPO are not set.
func createGitHubIssue(entry FeedbackEntry) (string, error) {
	token := os.Getenv("GITHUB_TOKEN")
	repo := os.Getenv("GITHUB_REPO") // e.g. "your-org/gocognigo"
	if token == "" || repo == "" {
		return "", nil
	}

	// Build issue title from first line / first 72 chars of the request
	title := entry.Text
	if idx := strings.IndexByte(title, '\n'); idx >= 0 {
		title = title[:idx]
	}
	if len(title) > 72 {
		title = title[:72] + "…"
	}
	title = "Feature Request: " + title

	// Build issue body
	body := fmt.Sprintf("## Feature Request\n\n%s\n\n---\n*Submitted via GoCognigo*", entry.Text)
	if entry.UserEmail != "" {
		body += fmt.Sprintf("  \n*From: %s*", entry.UserEmail)
	}

	payload, _ := json.Marshal(map[string]interface{}{
		"title":  title,
		"body":   body,
		"labels": []string{"feature-request"},
	})

	url := fmt.Sprintf("https://api.github.com/repos/%s/issues", repo)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("GitHub API returned %d", resp.StatusCode)
	}

	var result struct {
		HTMLURL string `json:"html_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}
	return result.HTMLURL, nil
}

// handleFeedback handles POST (submit) and GET (admin list) for feature requests.
func (s *Server) handleFeedback(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		s.submitFeedback(w, r)
	case http.MethodGet:
		s.listFeedback(w, r)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) submitFeedback(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Text == "" {
		jsonErr(w, "text is required", http.StatusBadRequest)
		return
	}

	entry := FeedbackEntry{
		ID:        newID(),
		Text:      req.Text,
		UserUID:   getUserUID(r),
		UserEmail: getUserEmail(r),
		CreatedAt: time.Now(),
	}

	// Fire GitHub issue creation in background so the user gets an instant response.
	go func() {
		issueURL, err := createGitHubIssue(entry)
		if err != nil {
			log.Printf("GitHub issue creation failed: %v", err)
			return
		}
		if issueURL == "" {
			return // GitHub not configured
		}
		log.Printf("Feature request → GitHub issue: %s", issueURL)

		// Persist the issue URL back into the entry.
		feedbackMu.Lock()
		defer feedbackMu.Unlock()
		entries, err := loadFeedback()
		if err != nil {
			return
		}
		for i := range entries {
			if entries[i].ID == entry.ID {
				entries[i].GitHubIssue = issueURL
				break
			}
		}
		_ = saveFeedback(entries)
	}()

	// Save entry immediately (without issue URL — it'll be patched in above).
	feedbackMu.Lock()
	defer feedbackMu.Unlock()

	entries, err := loadFeedback()
	if err != nil {
		jsonErr(w, "failed to load feedback store", http.StatusInternalServerError)
		return
	}
	entries = append(entries, entry)
	if err := saveFeedback(entries); err != nil {
		jsonErr(w, "failed to save feedback", http.StatusInternalServerError)
		return
	}

	jsonResp(w, map[string]string{"status": "submitted"})
}

func (s *Server) listFeedback(w http.ResponseWriter, r *http.Request) {
	// Only the configured admin UID can list feedback.
	adminUID := os.Getenv("ADMIN_UID")
	if adminUID == "" {
		jsonErr(w, "admin access not configured", http.StatusForbidden)
		return
	}
	uid := getUserUID(r)
	if uid != adminUID {
		jsonErr(w, "forbidden", http.StatusForbidden)
		return
	}

	feedbackMu.Lock()
	defer feedbackMu.Unlock()

	entries, err := loadFeedback()
	if err != nil {
		jsonErr(w, "failed to load feedback", http.StatusInternalServerError)
		return
	}
	if entries == nil {
		entries = []FeedbackEntry{}
	}
	jsonResp(w, entries)
}
