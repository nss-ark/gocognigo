package indexer

import (
	"bytes"
	"context"
	"encoding/gob"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"gocognigo/internal/extractor"

	"github.com/blevesearch/bleve/v2"
	"github.com/sashabaranov/go-openai"
)

// Section represents a named section within a document.
type Section struct {
	Name      string `json:"name"`
	PageStart int    `json:"page_start"`
	PageEnd   int    `json:"page_end"`
}

// DocumentSummary is the L0 document-level metadata (LLM-generated at ingest).
type DocumentSummary struct {
	Document    string    `json:"document"`
	Title       string    `json:"title"`
	DocType     string    `json:"type"`
	Summary     string    `json:"summary"`
	Sections    []Section `json:"sections"`
	KeyEntities []string  `json:"key_entities"`
}

// Chunk represents a piece of text to be embedded and indexed.
// Text is a small search chunk (~150 words); ParentText is the full page for LLM context.
type Chunk struct {
	ID         string    `json:"id"`
	Document   string    `json:"document"`
	PageNumber int       `json:"page_number"`
	Text       string    `json:"text"`        // small search chunk
	ParentText string    `json:"parent_text"` // full page text (sent to LLM)
	Section    string    `json:"section"`     // section name from doc summary
	Embedding  []float32 `json:"embedding"`
}

// EmbeddingProvider defines the interface for embeddings
type EmbeddingProvider interface {
	Embed(ctx context.Context, texts []string) ([][]float32, error)
}

// ProgressFunc is called during ingestion with (totalChunks, chunksDone).
type ProgressFunc func(total, done int)

type Index struct {
	Chunks       []Chunk
	DocSummaries []DocumentSummary
	BM25Index    bleve.Index
	Embedder     EmbeddingProvider
	mu           sync.Mutex // protects Chunks during concurrent writes
}

func NewIndex(providerName, apiKey, modelName, bm25Path string) (*Index, error) {
	var bmIndex bleve.Index
	var err error

	if _, statErr := os.Stat(bm25Path); os.IsNotExist(statErr) {
		mapping := bleve.NewIndexMapping()
		bmIndex, err = bleve.New(bm25Path, mapping)
		if err != nil {
			return nil, err
		}
	} else {
		bmIndex, err = bleve.Open(bm25Path)
		if err != nil {
			return nil, err
		}
	}

	var embedder EmbeddingProvider
	providerName = strings.ToLower(providerName)
	switch providerName {
	case "huggingface":
		if modelName == "" {
			modelName = "BAAI/bge-small-en-v1.5"
		}
		embedder = &HuggingFaceEmbedder{apiKey: apiKey, model: modelName}
	case "openai", "":
		if modelName == "" {
			modelName = "text-embedding-3-small"
		}
		embedder = &OpenAIEmbedder{client: openai.NewClient(apiKey), model: modelName}
	default:
		return nil, fmt.Errorf("unknown embedding provider: %s", providerName)
	}

	return &Index{
		Chunks:    []Chunk{},
		BM25Index: bmIndex,
		Embedder:  embedder,
	}, nil
}

// AddDocument is the backward-compatible wrapper (no progress callback).
func (idx *Index) AddDocument(ctx context.Context, docChunks []extractor.DocumentChunk) error {
	return idx.AddDocumentWithProgress(ctx, docChunks, nil)
}

// ChunkPages splits extracted document pages into small (~150-word) search chunks
// linked to their full-page parent text. This is a pure function on the Index
// (only reads DocSummaries for section lookup) and is safe to call concurrently.
func (idx *Index) ChunkPages(docChunks []extractor.DocumentChunk) []Chunk {
	var indexChunks []Chunk

	// Build section lookup from document summaries
	sectionMap := idx.buildSectionMap()

	for _, page := range docChunks {
		parentText := page.Text
		section := sectionMap.lookup(page.Document, page.PageNumber)
		words := strings.Fields(page.Text)
		chunkSize := 150
		overlap := 30

		for i := 0; i < len(words); i += (chunkSize - overlap) {
			end := i + chunkSize
			if end > len(words) {
				end = len(words)
			}
			textChunk := strings.Join(words[i:end], " ")

			id := fmt.Sprintf("%s_p%d_c%d", page.Document, page.PageNumber, len(indexChunks))

			indexChunks = append(indexChunks, Chunk{
				ID:         id,
				Document:   page.Document,
				PageNumber: page.PageNumber,
				Text:       textChunk,
				ParentText: parentText,
				Section:    section,
			})

			if end == len(words) {
				break
			}
		}
	}

	return indexChunks
}

// EmbedAndIndex embeds a slice of chunks and adds them to both the vector and BM25 indexes.
// It processes in batches of 200 with up to 6 concurrent API calls, with retry logic.
// Thread-safe: multiple goroutines can call this on the same Index.
func (idx *Index) EmbedAndIndex(ctx context.Context, chunks []Chunk, progress ProgressFunc, progressOffset int) error {
	if len(chunks) == 0 {
		return nil
	}

	totalChunks := len(chunks)

	// Build batch jobs — 200 per batch for good throughput
	batchSize := 200
	type batchJob struct {
		start int
		end   int
	}
	var jobs []batchJob
	for i := 0; i < len(chunks); i += batchSize {
		end := i + batchSize
		if end > len(chunks) {
			end = len(chunks)
		}
		jobs = append(jobs, batchJob{start: i, end: end})
	}

	// Run embedding batches with concurrency limit
	concurrency := 6
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	var firstErr error
	var errOnce sync.Once
	var doneCount int
	var doneMu sync.Mutex

	for _, job := range jobs {
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			errOnce.Do(func() { firstErr = ctx.Err() })
			break
		}
		wg.Add(1)

		go func(j batchJob) {
			defer wg.Done()
			defer func() { <-sem }()

			if ctx.Err() != nil {
				errOnce.Do(func() { firstErr = ctx.Err() })
				return
			}

			batch := chunks[j.start:j.end]
			var inputs []string
			for _, c := range batch {
				inputs = append(inputs, c.Text)
			}

			// Retry with exponential backoff (5 attempts)
			var embeddings [][]float32
			var err error
			for attempt := 0; attempt < 5; attempt++ {
				if ctx.Err() != nil {
					errOnce.Do(func() { firstErr = ctx.Err() })
					return
				}
				embeddings, err = idx.Embedder.Embed(ctx, inputs)
				if err == nil {
					break
				}
				if attempt < 4 {
					wait := time.Duration(3*(1<<uint(attempt))) * time.Second
					if wait > 20*time.Second {
						wait = 20 * time.Second
					}
					log.Printf("Embedding batch retry %d after %v: %v", attempt+1, wait, err)
					timer := time.NewTimer(wait)
					select {
					case <-timer.C:
					case <-ctx.Done():
						timer.Stop()
						errOnce.Do(func() { firstErr = ctx.Err() })
						return
					}
				}
			}
			if err != nil {
				errOnce.Do(func() {
					firstErr = fmt.Errorf("embedding error on batch: %w", err)
				})
				return
			}

			// Write results (thread-safe)
			idx.mu.Lock()
			for k, emb := range embeddings {
				batch[k].Embedding = emb
				idx.Chunks = append(idx.Chunks, batch[k])

				bm25Err := idx.BM25Index.Index(batch[k].ID, map[string]interface{}{
					"id":   batch[k].ID,
					"text": batch[k].Text,
					"doc":  batch[k].Document,
					"page": batch[k].PageNumber,
				})
				if bm25Err != nil {
					log.Printf("Failed to index BM25 for %s: %v", batch[k].ID, bm25Err)
				}
			}
			idx.mu.Unlock()

			doneMu.Lock()
			doneCount += len(batch)
			if progress != nil {
				progress(totalChunks, progressOffset+doneCount)
			}
			log.Printf("Embedded %d / %d chunks", doneCount, totalChunks)
			doneMu.Unlock()
		}(job)
	}

	wg.Wait()

	if firstErr != nil {
		return firstErr
	}

	return nil
}

// AddDocumentWithProgress processes document chunks with a progress callback.
// It chunks the text, then embeds batches concurrently with retry logic.
func (idx *Index) AddDocumentWithProgress(ctx context.Context, docChunks []extractor.DocumentChunk, progress ProgressFunc) error {
	indexChunks := idx.ChunkPages(docChunks)

	totalChunks := len(indexChunks)
	log.Printf("Chunking complete: %d chunks from %d pages", totalChunks, len(docChunks))
	if progress != nil {
		progress(totalChunks, 0)
	}

	return idx.EmbedAndIndex(ctx, indexChunks, progress, 0)
}

// sectionLookup maps document+page to section names.
type sectionLookup struct {
	summaries []DocumentSummary
}

func (idx *Index) buildSectionMap() sectionLookup {
	return sectionLookup{summaries: idx.DocSummaries}
}

func (sl sectionLookup) lookup(doc string, page int) string {
	for _, s := range sl.summaries {
		if s.Document != doc {
			continue
		}
		for _, sec := range s.Sections {
			if page >= sec.PageStart && page <= sec.PageEnd {
				return sec.Name
			}
		}
	}
	return ""
}

// vectorStore wraps chunks and summaries for serialization.
type vectorStore struct {
	Chunks       []Chunk           `json:"chunks"`
	DocSummaries []DocumentSummary `json:"doc_summaries,omitempty"`
}

// Save Vector index to disk in both binary (fast) and JSON (fallback) formats.
func (idx *Index) SaveVectors(path string) error {
	store := vectorStore{
		Chunks:       idx.Chunks,
		DocSummaries: idx.DocSummaries,
	}

	// Save binary format (primary — 5-10x faster to load)
	gobPath := strings.TrimSuffix(path, ".json") + ".gob"
	if err := idx.saveVectorsBinary(gobPath, store); err != nil {
		log.Printf("Warning: failed to save binary vectors: %v", err)
	} else {
		log.Printf("Saved binary vectors: %s", gobPath)
	}

	// Also save JSON format (backward compat)
	data, err := json.Marshal(store)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

func (idx *Index) saveVectorsBinary(path string, store vectorStore) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return gob.NewEncoder(f).Encode(store)
}

// Load Vectors from disk — tries binary (fast) first, falls back to JSON.
func (idx *Index) LoadVectors(path string) error {
	start := time.Now()

	// Try binary format first (5-10x faster)
	gobPath := strings.TrimSuffix(path, ".json") + ".gob"
	if _, err := os.Stat(gobPath); err == nil {
		if err := idx.loadVectorsBinary(gobPath); err == nil {
			log.Printf("Loaded %d chunks from binary in %v", len(idx.Chunks), time.Since(start))
			return nil
		}
		log.Printf("Binary load failed, falling back to JSON: %v", err)
	}

	// Fallback: JSON format
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	// Try new format (with summaries) first
	var store vectorStore
	if err := json.Unmarshal(data, &store); err == nil && len(store.Chunks) > 0 {
		idx.Chunks = store.Chunks
		idx.DocSummaries = store.DocSummaries
		log.Printf("Loaded %d chunks from JSON in %v", len(idx.Chunks), time.Since(start))
		return nil
	}
	// Fallback: legacy format (just chunks array)
	if err := json.Unmarshal(data, &idx.Chunks); err != nil {
		return err
	}
	log.Printf("Loaded %d chunks from legacy JSON in %v", len(idx.Chunks), time.Since(start))
	return nil
}

func (idx *Index) loadVectorsBinary(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	var store vectorStore
	if err := gob.NewDecoder(f).Decode(&store); err != nil {
		return err
	}
	idx.Chunks = store.Chunks
	idx.DocSummaries = store.DocSummaries
	return nil
}

// AddDocSummary appends a document summary in a thread-safe way.
func (idx *Index) AddDocSummary(summary DocumentSummary) {
	idx.mu.Lock()
	idx.DocSummaries = append(idx.DocSummaries, summary)
	idx.mu.Unlock()
}

// Close closes the BM25 index. Must be called before opening a different index.
func (idx *Index) Close() error {
	if idx.BM25Index != nil {
		return idx.BM25Index.Close()
	}
	return nil
}

// ==========================================
// OpenAI Embedder
// ==========================================
type OpenAIEmbedder struct {
	client *openai.Client
	model  string
}

func (e *OpenAIEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	resp, err := e.client.CreateEmbeddings(ctx, openai.EmbeddingRequest{
		Input: texts,
		Model: openai.EmbeddingModel(e.model),
	})
	if err != nil {
		return nil, err
	}

	var results [][]float32
	for _, d := range resp.Data {
		results = append(results, d.Embedding)
	}
	return results, nil
}

// ==========================================
// HuggingFace Embedder
// ==========================================
type HuggingFaceEmbedder struct {
	apiKey string
	model  string
}

func (e *HuggingFaceEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	reqBody, _ := json.Marshal(map[string]interface{}{
		"inputs": texts,
	})

	url := fmt.Sprintf("https://router.huggingface.co/models/%s", e.model)
	req, _ := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(reqBody))
	req.Header.Set("Authorization", "Bearer "+e.apiKey)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("HF api error: %d - %s", resp.StatusCode, string(bodyBytes))
	}

	var hfResp [][]float64
	if err := json.NewDecoder(resp.Body).Decode(&hfResp); err != nil {
		return nil, err
	}

	var results [][]float32
	for _, vec := range hfResp {
		var f32vec []float32
		for _, val := range vec {
			f32vec = append(f32vec, float32(val))
		}
		results = append(results, f32vec)
	}

	return results, nil
}
