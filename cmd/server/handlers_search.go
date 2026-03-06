package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"gocognigo/internal/indexer"

	"github.com/blevesearch/bleve/v2"
)

// handleSearch performs BM25 keyword search across indexed document chunks.
// No LLM or embedding API calls needed — pure keyword matching, instant results.
func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Query     string `json:"query"`
		ProjectID string `json:"project_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Query == "" || req.ProjectID == "" {
		jsonErr(w, "query and project_id are required", http.StatusBadRequest)
		return
	}

	start := time.Now()

	// Get the index for this project
	s.mu.RLock()
	var bm25Index bleve.Index
	var chunks []indexer.Chunk

	if s.activeProjectID == req.ProjectID && s.activeIndex != nil {
		bm25Index = s.activeIndex.BM25Index
		chunks = s.activeIndex.Chunks
	} else if cached, ok := s.indexCache.get(req.ProjectID); ok {
		bm25Index = cached.idx.BM25Index
		chunks = cached.idx.Chunks
	}
	s.mu.RUnlock()

	if bm25Index == nil || len(chunks) == 0 {
		jsonErr(w, "No documents indexed for this project", http.StatusBadRequest)
		return
	}

	// Build chunk lookup map
	chunkMap := make(map[string]indexer.Chunk, len(chunks))
	for _, c := range chunks {
		chunkMap[c.ID] = c
	}

	// Perform BM25 search
	q := bleve.NewMatchQuery(req.Query)
	searchReq := bleve.NewSearchRequest(q)
	searchReq.Size = 30

	bm25Results, err := bm25Index.Search(searchReq)
	if err != nil {
		jsonErr(w, fmt.Sprintf("Search error: %v", err), http.StatusInternalServerError)
		return
	}

	// Build results with highlighted text
	queryTerms := extractTerms(req.Query)
	var results []searchResult
	for _, hit := range bm25Results.Hits {
		chunk, ok := chunkMap[hit.ID]
		if !ok {
			continue
		}

		highlighted := highlightTerms(chunk.Text, queryTerms)

		results = append(results, searchResult{
			Document:   chunk.Document,
			PageNumber: chunk.PageNumber,
			Text:       highlighted,
			Section:    chunk.Section,
			Score:      hit.Score,
		})
	}

	elapsed := time.Since(start).Milliseconds()

	jsonResp(w, map[string]interface{}{
		"results": results,
		"total":   int(bm25Results.Total),
		"time_ms": elapsed,
	})
}

type searchResult struct {
	Document   string  `json:"document"`
	PageNumber int     `json:"page"`
	Text       string  `json:"text"`
	Section    string  `json:"section,omitempty"`
	Score      float64 `json:"score"`
}

// extractTerms splits a query into individual search terms (lowercase).
func extractTerms(query string) []string {
	words := strings.Fields(strings.ToLower(query))
	var terms []string
	for _, w := range words {
		w = strings.Trim(w, ".,;:!?\"'()[]{}")
		if len(w) >= 2 {
			terms = append(terms, w)
		}
	}
	return terms
}

// highlightTerms wraps matching terms in <mark> tags for display.
func highlightTerms(text string, terms []string) string {
	if len(terms) == 0 {
		return text
	}
	for _, term := range terms {
		pattern := regexp.MustCompile(`(?i)\b(` + regexp.QuoteMeta(term) + `\w*)`)
		text = pattern.ReplaceAllString(text, "<mark>$1</mark>")
	}
	return text
}
