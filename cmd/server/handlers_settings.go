package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"

	"gocognigo/internal/indexer"
	"gocognigo/internal/retriever"
)

// ========== Settings Endpoint ==========

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.mu.RLock()
		resp := map[string]interface{}{
			"default_llm":         s.defaultLLM,
			"embed_provider":      s.embedProvider,
			"openai_key":          maskKey(s.providerKeys["openai"]),
			"anthropic_key":       maskKey(s.providerKeys["anthropic"]),
			"huggingface_key":     maskKey(s.providerKeys["huggingface"]),
			"ocr_provider":        s.ocrProvider,
			"sarvam_key":          maskKey(s.sarvamAPIKey),
			"tesseract_available": s.tesseractOk,
		}
		s.mu.RUnlock()
		jsonResp(w, resp)

	case http.MethodPost:
		var req struct {
			OpenAIKey      string `json:"openai_key"`
			AnthropicKey   string `json:"anthropic_key"`
			HuggingFaceKey string `json:"huggingface_key"`
			DefaultLLM     string `json:"default_llm"`
			EmbedProvider  string `json:"embed_provider"`
			OCRProvider    string `json:"ocr_provider"`
			SarvamKey      string `json:"sarvam_key"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonErr(w, "Invalid request", http.StatusBadRequest)
			return
		}

		s.mu.Lock()
		if req.OpenAIKey != "" && !strings.Contains(req.OpenAIKey, "...") {
			s.providerKeys["openai"] = req.OpenAIKey
		}
		if req.AnthropicKey != "" && !strings.Contains(req.AnthropicKey, "...") {
			s.providerKeys["anthropic"] = req.AnthropicKey
		}
		if req.HuggingFaceKey != "" && !strings.Contains(req.HuggingFaceKey, "...") {
			s.providerKeys["huggingface"] = req.HuggingFaceKey
		}
		if req.DefaultLLM != "" {
			s.defaultLLM = req.DefaultLLM
		}
		if req.EmbedProvider != "" {
			s.embedProvider = req.EmbedProvider
			switch req.EmbedProvider {
			case "openai":
				s.embedAPIKey = s.providerKeys["openai"]
			case "huggingface":
				s.embedAPIKey = s.providerKeys["huggingface"]
			}
		}

		s.ocrProvider = req.OCRProvider
		if req.SarvamKey != "" && !strings.Contains(req.SarvamKey, "...") {
			s.sarvamAPIKey = req.SarvamKey
		}

		saved := SavedSettings{
			OpenAIKey:      s.providerKeys["openai"],
			AnthropicKey:   s.providerKeys["anthropic"],
			HuggingFaceKey: s.providerKeys["huggingface"],
			DefaultLLM:     s.defaultLLM,
			EmbedProvider:  s.embedProvider,
			OCRProvider:    s.ocrProvider,
			SarvamKey:      s.sarvamAPIKey,
		}
		s.mu.Unlock()

		if err := persistSettings(saved); err != nil {
			log.Printf("Failed to persist settings: %v", err)
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
func (s *Server) loadChatIndexes(ProjectID string) error {
	bm25Dir := s.projects.BM25Dir(ProjectID)
	vectorsPath := s.projects.VectorsPath(ProjectID)

	if _, err := os.Stat(vectorsPath); os.IsNotExist(err) {
		return fmt.Errorf("no vectors file for project %s", ProjectID)
	}

	idx, err := indexer.NewIndex(s.embedProvider, s.embedAPIKey, "", bm25Dir)
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
