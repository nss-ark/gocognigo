package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"gocognigo/internal/chat"
	"gocognigo/internal/indexer"
	"gocognigo/internal/retriever"
)

// ========== Settings Endpoint ==========

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		settings := s.getUserSettings(r)
		resp := map[string]interface{}{
			"default_llm":         settings.DefaultLLM,
			"embed_provider":      settings.EmbedProvider,
			"embed_model":         settings.EmbedModel,
			"openai_key":          maskKey(settings.OpenAIKey),
			"anthropic_key":       maskKey(settings.AnthropicKey),
			"huggingface_key":     maskKey(settings.HuggingFaceKey),
			"ocr_provider":        settings.OCRProvider,
			"sarvam_key":          maskKey(settings.SarvamKey),
			"tesseract_available": s.tesseractOk, // immutable after startup
			"tesseract_lang":      settings.TesseractLang,
		}
		jsonResp(w, resp)

	case http.MethodPost:
		var req struct {
			OpenAIKey      string `json:"openai_key"`
			AnthropicKey   string `json:"anthropic_key"`
			HuggingFaceKey string `json:"huggingface_key"`
			DefaultLLM     string `json:"default_llm"`
			EmbedProvider  string `json:"embed_provider"`
			EmbedModel     string `json:"embed_model"`
			OCRProvider    string `json:"ocr_provider"`
			SarvamKey      string `json:"sarvam_key"`
			TesseractLang  string `json:"tesseract_lang"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonErr(w, "Invalid request", http.StatusBadRequest)
			return
		}

		settings := s.getUserSettings(r)

		// Create a copy to update
		newSettings := *settings

		if req.OpenAIKey != "" && !strings.Contains(req.OpenAIKey, "...") {
			newSettings.OpenAIKey = req.OpenAIKey
		}
		if req.AnthropicKey != "" && !strings.Contains(req.AnthropicKey, "...") {
			newSettings.AnthropicKey = req.AnthropicKey
		}
		if req.HuggingFaceKey != "" && !strings.Contains(req.HuggingFaceKey, "...") {
			newSettings.HuggingFaceKey = req.HuggingFaceKey
		}
		if req.DefaultLLM != "" {
			newSettings.DefaultLLM = req.DefaultLLM
		}
		if req.EmbedProvider != "" {
			newSettings.EmbedProvider = req.EmbedProvider
		}
		if req.EmbedModel != "" { // Can be empty to mean default
			newSettings.EmbedModel = req.EmbedModel
		} else {
			newSettings.EmbedModel = "" // support clearing
		}

		newSettings.OCRProvider = req.OCRProvider
		if req.SarvamKey != "" && !strings.Contains(req.SarvamKey, "...") {
			newSettings.SarvamKey = req.SarvamKey
		}
		if req.TesseractLang != "" {
			newSettings.TesseractLang = req.TesseractLang
		}

		if err := s.saveUserSettings(r, &newSettings); err != nil {
			log.Printf("Failed to persist settings: %v", err)
			jsonErr(w, "Failed to persist settings", http.StatusInternalServerError)
			return
		}

		log.Printf("Settings updated: LLM=%s, Embed=%s", req.DefaultLLM, req.EmbedProvider)
		jsonResp(w, map[string]string{"status": "saved"})

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleIndexStatus returns whether the vector index is loaded for a given project.
func (s *Server) handleIndexStatus(w http.ResponseWriter, r *http.Request) {
	projectID := r.URL.Query().Get("project_id")

	s.mu.RLock()
	loading := s.indexLoading

	ready := false
	if projectID != "" {
		// Check if this specific project has a loaded index
		if s.activeProjectID == projectID && s.activeRetriever != nil {
			ready = true
		} else if s.indexCache.has(projectID) {
			ready = true
		}
	} else {
		ready = s.activeRetriever != nil
	}
	s.mu.RUnlock()

	status := "ready"
	if loading {
		status = "loading"
	} else if !ready {
		status = "not_loaded"
	}

	jsonResp(w, map[string]interface{}{
		"status": status,
		"ready":  ready,
	})
}

// loadChatIndexes loads a project's pre-built indexes from disk.
func (s *Server) loadChatIndexes(store *chat.ProjectStore, settings *SavedSettings, ProjectID string) error {
	bm25Dir := store.BM25Dir(ProjectID)
	vectorsPath := store.VectorsPath(ProjectID)

	if _, err := os.Stat(vectorsPath); os.IsNotExist(err) {
		return fmt.Errorf("no vectors file for project %s", ProjectID)
	}

	idx, err := indexer.NewIndex(settings.EmbedProvider, settings.OpenAIKey, settings.EmbedModel, bm25Dir)
	if err != nil {
		return fmt.Errorf("failed to open BM25 index: %w", err)
	}

	if err := idx.LoadVectors(vectorsPath); err != nil {
		_ = idx.Close()
		return fmt.Errorf("failed to load vectors: %w", err)
	}

	ret := retriever.NewRetriever(idx)

	s.mu.Lock()
	s.activeIndex = idx
	s.activeRetriever = ret
	s.activeProjectID = ProjectID
	s.indexCache.put(ProjectID, &cachedIndex{idx: idx, ret: ret})
	s.mu.Unlock()

	log.Printf("Loaded %d chunks for project %s (cached)", len(idx.Chunks), ProjectID)
	return nil
}

// handleValidateKey tests an API key with a minimal API call.
func (s *Server) handleValidateKey(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Provider string `json:"provider"`
		APIKey   string `json:"api_key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Provider == "" {
		jsonErr(w, "provider is required", http.StatusBadRequest)
		return
	}

	// If no key provided, use the stored key from user settings.
	// Note: getUserSettings manages its own locking — do NOT hold s.mu here.
	apiKey := req.APIKey
	if apiKey == "" {
		settings := s.getUserSettings(r)
		switch strings.ToLower(req.Provider) {
		case "openai":
			apiKey = settings.OpenAIKey
		case "anthropic":
			apiKey = settings.AnthropicKey
		case "huggingface":
			apiKey = settings.HuggingFaceKey
		case "sarvam":
			apiKey = settings.SarvamKey
		}
		if apiKey == "" {
			jsonResp(w, map[string]interface{}{"valid": false, "error": "No API key configured for " + req.Provider})
			return
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	var valid bool
	var errMsg string

	switch strings.ToLower(req.Provider) {
	case "openai":
		valid, errMsg = validateOpenAIKey(ctx, apiKey)
	case "anthropic":
		valid, errMsg = validateAnthropicKey(ctx, apiKey)
	case "huggingface":
		valid, errMsg = validateHuggingFaceKey(ctx, apiKey)
	case "sarvam":
		valid, errMsg = validateSarvamKey(ctx, apiKey)
	default:
		jsonErr(w, "Unknown provider: "+req.Provider, http.StatusBadRequest)
		return
	}

	jsonResp(w, map[string]interface{}{
		"valid": valid,
		"error": errMsg,
	})
}

// validateOpenAIKey uses the models list endpoint — cheapest possible call.
func validateOpenAIKey(ctx context.Context, apiKey string) (bool, string) {
	req, _ := http.NewRequestWithContext(ctx, "GET", "https://api.openai.com/v1/models", nil)
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false, "Connection error: " + err.Error()
	}
	defer resp.Body.Close()

	if resp.StatusCode == 200 {
		return true, ""
	}
	if resp.StatusCode == 401 {
		return false, "Invalid API key"
	}
	body, _ := io.ReadAll(resp.Body)
	return false, fmt.Sprintf("HTTP %d: %s", resp.StatusCode, truncateStr(string(body), 200))
}

// validateAnthropicKey sends a minimal messages request (max_tokens=1, tiny prompt).
func validateAnthropicKey(ctx context.Context, apiKey string) (bool, string) {
	reqBody, _ := json.Marshal(map[string]interface{}{
		"model":      "claude-3-haiku-20240307",
		"max_tokens": 1,
		"messages": []map[string]string{
			{"role": "user", "content": "hi"},
		},
	})

	req, _ := http.NewRequestWithContext(ctx, "POST", "https://api.anthropic.com/v1/messages", bytes.NewBuffer(reqBody))
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("content-type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false, "Connection error: " + err.Error()
	}
	defer resp.Body.Close()

	if resp.StatusCode == 200 {
		return true, ""
	}
	if resp.StatusCode == 401 {
		return false, "Invalid API key"
	}
	body, _ := io.ReadAll(resp.Body)
	return false, fmt.Sprintf("HTTP %d: %s", resp.StatusCode, truncateStr(string(body), 200))
}

// validateHuggingFaceKey checks the token via the whoami endpoint.
func validateHuggingFaceKey(ctx context.Context, apiKey string) (bool, string) {
	req, _ := http.NewRequestWithContext(ctx, "GET", "https://huggingface.co/api/whoami-v2", nil)
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false, "Connection error: " + err.Error()
	}
	defer resp.Body.Close()

	if resp.StatusCode == 200 {
		return true, ""
	}
	if resp.StatusCode == 401 {
		return false, "Invalid API key"
	}
	body, _ := io.ReadAll(resp.Body)
	return false, fmt.Sprintf("HTTP %d: %s", resp.StatusCode, truncateStr(string(body), 200))
}

// validateSarvamKey tests the Sarvam Vision OCR API.
func validateSarvamKey(ctx context.Context, apiKey string) (bool, string) {
	// Sarvam doesn't have a simple health-check endpoint, so we validate the key
	// by trying a minimal request that will fail fast with auth errors
	req, _ := http.NewRequestWithContext(ctx, "GET", "https://api.sarvam.ai/v1/models", nil)
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false, "Connection error: " + err.Error()
	}
	defer resp.Body.Close()

	if resp.StatusCode == 200 {
		return true, ""
	}
	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		return false, "Invalid API key"
	}
	// For Sarvam, anything else we treat as potentially valid (API quirks)
	return true, ""
}

func truncateStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
