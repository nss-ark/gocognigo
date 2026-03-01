package retriever

import (
	"context"
	"fmt"
	"math"
	"sort"

	"gocognigo/internal/indexer"

	"github.com/blevesearch/bleve/v2"
)

// Result represents a retrieved chunk with its relevance score
type Result struct {
	ChunkID    string  `json:"chunk_id"`
	Document   string  `json:"document"`
	PageNumber int     `json:"page_number"`
	Text       string  `json:"text"`        // small search chunk text
	ParentText string  `json:"parent_text"` // full page text for LLM context
	Section    string  `json:"section"`     // section name from document summary
	Score      float64 `json:"score"`
}

// Retriever performs hybrid search over vector and BM25 indexes
type Retriever struct {
	Chunks       []indexer.Chunk
	DocSummaries []indexer.DocumentSummary
	BM25Index    bleve.Index
	Embedder     indexer.EmbeddingProvider
}

// NewRetriever creates a Retriever from a pre-built Index
func NewRetriever(idx *indexer.Index) *Retriever {
	return &Retriever{
		Chunks:       idx.Chunks,
		DocSummaries: idx.DocSummaries,
		BM25Index:    idx.BM25Index,
		Embedder:     idx.Embedder,
	}
}

// Search performs hybrid retrieval: vector similarity + BM25, merged via Reciprocal Rank Fusion.
// Results are deduplicated by parent page — if multiple small chunks from the same page match,
// only the highest-scored one is kept (but the full parent page text is returned for LLM context).
func (r *Retriever) Search(ctx context.Context, query string, topK int) ([]Result, error) {
	// 1. Embed the query
	resp, err := r.Embedder.Embed(ctx, []string{query})
	if err != nil {
		return nil, fmt.Errorf("query embedding error: %w", err)
	}
	queryEmb := resp[0]

	// 2. Vector search — cosine similarity
	type scored struct {
		idx   int
		score float64
	}
	var vectorScores []scored
	for i, chunk := range r.Chunks {
		sim := cosineSimilarity(queryEmb, chunk.Embedding)
		vectorScores = append(vectorScores, scored{i, sim})
	}
	sort.Slice(vectorScores, func(i, j int) bool {
		return vectorScores[i].score > vectorScores[j].score
	})

	// 3. BM25 search
	bm25Query := bleve.NewMatchQuery(query)
	searchReq := bleve.NewSearchRequest(bm25Query)
	searchReq.Size = topK * 3 // Get more candidates for fusion
	bm25Results, err := r.BM25Index.Search(searchReq)
	if err != nil {
		return nil, fmt.Errorf("BM25 search error: %w", err)
	}

	// 4. Build chunk ID → rank maps for RRF
	vectorRanks := make(map[string]int)
	limit := topK * 3
	if limit > len(vectorScores) {
		limit = len(vectorScores)
	}
	for rank, s := range vectorScores[:limit] {
		vectorRanks[r.Chunks[s.idx].ID] = rank + 1
	}

	bm25Ranks := make(map[string]int)
	for rank, hit := range bm25Results.Hits {
		bm25Ranks[hit.ID] = rank + 1
	}

	// 5. Reciprocal Rank Fusion (k=60)
	const k = 60.0
	allIDs := make(map[string]bool)
	for id := range vectorRanks {
		allIDs[id] = true
	}
	for id := range bm25Ranks {
		allIDs[id] = true
	}

	type fusedResult struct {
		id    string
		score float64
	}
	var fused []fusedResult
	for id := range allIDs {
		score := 0.0
		if vr, ok := vectorRanks[id]; ok {
			score += 1.0 / (k + float64(vr))
		}
		if br, ok := bm25Ranks[id]; ok {
			score += 1.0 / (k + float64(br))
		}
		fused = append(fused, fusedResult{id, score})
	}
	sort.Slice(fused, func(i, j int) bool {
		return fused[i].score > fused[j].score
	})

	// 6. Build result list with parent-page deduplication
	chunkMap := make(map[string]indexer.Chunk)
	for _, c := range r.Chunks {
		chunkMap[c.ID] = c
	}

	// Deduplicate: keep only the best-scoring chunk per parent page (doc+page)
	seen := make(map[string]bool) // "document_pageN" → already included
	var results []Result
	for _, f := range fused {
		if len(results) >= topK {
			break
		}
		chunk, ok := chunkMap[f.id]
		if !ok {
			continue
		}
		parentKey := fmt.Sprintf("%s_p%d", chunk.Document, chunk.PageNumber)
		if seen[parentKey] {
			continue // already have a chunk from this page
		}
		seen[parentKey] = true

		results = append(results, Result{
			ChunkID:    chunk.ID,
			Document:   chunk.Document,
			PageNumber: chunk.PageNumber,
			Text:       chunk.Text,
			ParentText: chunk.ParentText,
			Section:    chunk.Section,
			Score:      f.score,
		})
	}

	return results, nil
}

func cosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		normA += float64(a[i]) * float64(a[i])
		normB += float64(b[i]) * float64(b[i])
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}
