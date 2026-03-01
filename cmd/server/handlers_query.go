package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"gocognigo/internal/chat"
	"gocognigo/internal/indexer"
	"gocognigo/internal/llm"
	"gocognigo/internal/retriever"
)

// ========== Query Endpoints ==========

// getRetrieverForProject returns the retriever for a project, checking cache.
func (s *Server) getRetrieverForProject(projectID string) (*retriever_wrapper, error) {
	s.mu.RLock()
	// Check if already active
	if s.activeProjectID == projectID && s.activeRetriever != nil {
		ret := s.activeRetriever
		idx := s.activeIndex
		s.mu.RUnlock()
		return &retriever_wrapper{ret: ret, idx: idx}, nil
	}
	// Check cache
	if cached, ok := s.indexCache.get(projectID); ok {
		s.mu.RUnlock()
		return &retriever_wrapper{ret: cached.ret, idx: cached.idx}, nil
	}
	s.mu.RUnlock()
	return nil, fmt.Errorf("no index loaded for project %s", projectID)
}

type retriever_wrapper struct {
	ret *retriever.Retriever
	idx *indexer.Index
}

func (s *Server) handleQuery(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req QueryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonErr(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.ProjectID == "" {
		jsonErr(w, "project_id is required", http.StatusBadRequest)
		return
	}

	rw, err := s.getRetrieverForProject(req.ProjectID)
	if err != nil {
		jsonErr(w, "No documents indexed. Upload and process documents first.", http.StatusBadRequest)
		return
	}

	llmClient, err := s.getProvider(req.Provider, req.Model)
	if err != nil {
		jsonErr(w, fmt.Sprintf("Provider error: %v", err), http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	start := time.Now()

	results, err := rw.ret.Search(ctx, req.Question, 20)
	if err != nil {
		jsonErr(w, fmt.Sprintf("Retrieval error: %v", err), http.StatusInternalServerError)
		return
	}

	answer, err := llmClient.AnswerQuestion(ctx, req.Question, results, rw.ret.DocSummaries)
	if err != nil {
		jsonErr(w, fmt.Sprintf("LLM error: %v", err), http.StatusInternalServerError)
		return
	}

	elapsed := time.Since(start).Seconds()

	// Persist messages to conversation if IDs are provided
	if req.ConversationID != "" {
		userMsg := chat.Message{
			Role:      "user",
			Content:   req.Question,
			Timestamp: start,
		}
		assistantMsg := chat.Message{
			Role:    "assistant",
			Content: answer.Answer,
			Metadata: map[string]interface{}{
				"thinking":          answer.Thinking,
				"documents":         answer.Documents,
				"pages":             answer.Pages,
				"footnotes":         answer.Footnotes,
				"confidence":        answer.Confidence,
				"confidence_reason": answer.ConfidenceReason,
				"time_seconds":      elapsed,
				"provider":          req.Provider,
				"model":             req.Model,
			},
			Timestamp: time.Now(),
		}
		go func() {
			_ = s.projects.SaveMessage(req.ProjectID, req.ConversationID, userMsg)
			_ = s.projects.SaveMessage(req.ProjectID, req.ConversationID, assistantMsg)
		}()
	}

	jsonResp(w, map[string]interface{}{
		"answer":       answer,
		"time_seconds": elapsed,
	})
}

func (s *Server) handleBatch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req BatchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonErr(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.ProjectID == "" {
		jsonErr(w, "project_id is required", http.StatusBadRequest)
		return
	}

	rw, err := s.getRetrieverForProject(req.ProjectID)
	if err != nil {
		jsonErr(w, "No documents indexed. Upload and process documents first.", http.StatusBadRequest)
		return
	}

	llmClient, err := s.getProvider(req.Provider, req.Model)
	if err != nil {
		jsonErr(w, fmt.Sprintf("Provider error: %v", err), http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	start := time.Now()

	answers := make([]*llm.Answer, len(req.Questions))
	var wg sync.WaitGroup
	var mu sync.Mutex
	var errors []string

	for i, q := range req.Questions {
		wg.Add(1)
		go func(idx int, question string) {
			defer wg.Done()
			results, err := rw.ret.Search(ctx, question, 20)
			if err != nil {
				mu.Lock()
				errors = append(errors, fmt.Sprintf("Q%d retrieval: %v", idx, err))
				mu.Unlock()
				return
			}
			answer, err := llmClient.AnswerQuestion(ctx, question, results, rw.ret.DocSummaries)
			if err != nil {
				mu.Lock()
				errors = append(errors, fmt.Sprintf("Q%d LLM: %v", idx, err))
				mu.Unlock()
				return
			}
			mu.Lock()
			answers[idx] = answer
			mu.Unlock()
		}(i, q)
	}
	wg.Wait()

	jsonResp(w, BatchResponse{
		Answers:   answers,
		TotalTime: time.Since(start).Seconds(),
	})
}

// ========== Stats & Providers ==========

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	// Optionally accept project_id to get project-specific stats
	projectID := r.URL.Query().Get("project_id")

	s.mu.RLock()
	idx := s.activeIndex
	currentProjectID := s.activeProjectID
	s.mu.RUnlock()

	// If a specific project is requested and it's in cache, use that
	if projectID != "" && projectID != currentProjectID {
		s.mu.RLock()
		if cached, ok := s.indexCache.get(projectID); ok {
			idx = cached.idx
		} else {
			idx = nil
		}
		s.mu.RUnlock()
	}

	docs := 0
	chunks := 0
	if idx != nil {
		docSet := make(map[string]bool)
		for _, c := range idx.Chunks {
			docSet[c.Document] = true
		}
		docs = len(docSet)
		chunks = len(idx.Chunks)
	}

	var available []string
	for name, key := range s.providerKeys {
		if key != "" && key != "your_openai_key_here" && key != "your_anthropic_key_here" {
			available = append(available, name)
		}
	}

	resp := StatsResponse{
		Documents:  docs,
		Chunks:     chunks,
		IndexReady: chunks > 0,
		Providers:  available,
		DefaultLLM: s.defaultLLM,
	}

	jsonResp(w, resp)
}

func (s *Server) handleProviders(w http.ResponseWriter, r *http.Request) {
	allModels := map[string][]map[string]string{
		"anthropic": {
			{"id": "claude-opus-4-6", "name": "Claude Opus 4.6 (Latest)"},
			{"id": "claude-sonnet-4-6", "name": "Claude Sonnet 4.6"},
			{"id": "claude-opus-4-5-20251101", "name": "Claude Opus 4.5"},
			{"id": "claude-opus-4-1-20250805", "name": "Claude Opus 4.1"},
			{"id": "claude-sonnet-4-20250514", "name": "Claude Sonnet 4"},
			{"id": "claude-haiku-4-5-20251001", "name": "Claude Haiku 4.5"},
			{"id": "claude-3-5-sonnet-20241022", "name": "Claude 3.5 Sonnet"},
			{"id": "claude-3-opus-20240229", "name": "Claude 3 Opus"},
		},
		"huggingface": {
			{"id": "Qwen/Qwen2.5-72B-Instruct", "name": "Qwen 2.5 72B Instruct"},
			{"id": "Qwen/Qwen3-8B", "name": "Qwen 3 8B"},
			{"id": "Qwen/QwQ-32B", "name": "QwQ 32B (Reasoning)"},
			{"id": "meta-llama/Llama-3.3-70B-Instruct", "name": "Llama 3.3 70B Instruct"},
			{"id": "microsoft/phi-4", "name": "Phi-4 14B"},
			{"id": "Qwen/Qwen2.5-Coder-32B-Instruct", "name": "Qwen 2.5 Coder 32B"},
		},
	}

	result := make(map[string]interface{})
	for name, key := range s.providerKeys {
		if key != "" && key != "your_openai_key_here" && key != "your_anthropic_key_here" {
			result[name] = allModels[name]
		}
	}
	jsonResp(w, result)
}
