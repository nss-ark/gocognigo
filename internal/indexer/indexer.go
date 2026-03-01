package indexer

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

// AddDocumentWithProgress processes document chunks with a progress callback.
// It chunks the text, then embeds batches concurrently (up to 3 parallel) with retry logic.
func (idx *Index) AddDocumentWithProgress(ctx context.Context, docChunks []extractor.DocumentChunk, progress ProgressFunc) error {
	var indexChunks []Chunk

	// Build section lookup from document summaries
	sectionMap := idx.buildSectionMap()

	// Chunking: small 150-word search chunks linked to full-page parent text
	for _, page := range docChunks {
		parentText := page.Text // full page = parent chunk (L1)
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

	totalChunks := len(indexChunks)
	log.Printf("Chunking complete: %d chunks from %d pages", totalChunks, len(docChunks))
	if progress != nil {
		progress(totalChunks, 0)
	}

	// Build batch jobs — larger batches for better throughput
	// OpenAI supports up to 2048 inputs; HuggingFace free tier needs smaller batches
	batchSize := 100
	type batchJob struct {
		start int
		end   int
	}
	var jobs []batchJob
	for i := 0; i < len(indexChunks); i += batchSize {
		end := i + batchSize
		if end > len(indexChunks) {
			end = len(indexChunks)
		}
		jobs = append(jobs, batchJob{start: i, end: end})
	}

	// Run embedding batches with concurrency limit
	concurrency := 4
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	var firstErr error
	var errOnce sync.Once
	var doneCount int
	var doneMu sync.Mutex

	for _, job := range jobs {
		// Acquire slot with cancellation check
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			errOnce.Do(func() { firstErr = ctx.Err() })
			break
		}
		wg.Add(1)

		go func(j batchJob) {
			defer wg.Done()
			defer func() { <-sem }() // release slot

			// Check context at batch start
			if ctx.Err() != nil {
				errOnce.Do(func() { firstErr = ctx.Err() })
				return
			}

			batch := indexChunks[j.start:j.end]
			var inputs []string
			for _, c := range batch {
				inputs = append(inputs, c.Text)
			}

			// Retry with exponential backoff (5 attempts, longer waits for free-tier APIs)
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
					// Backoff: 3s, 6s, 12s, 20s — but cancel-aware
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

				// Add to BM25 index
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
				progress(totalChunks, doneCount)
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

// Save Vector index to disk (since BM25 is already on disk via Bleve)
func (idx *Index) SaveVectors(path string) error {
	store := vectorStore{
		Chunks:       idx.Chunks,
		DocSummaries: idx.DocSummaries,
	}
	data, err := json.Marshal(store)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// Load Vectors from disk
func (idx *Index) LoadVectors(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	// Try new format (with summaries) first
	var store vectorStore
	if err := json.Unmarshal(data, &store); err == nil && len(store.Chunks) > 0 {
		idx.Chunks = store.Chunks
		idx.DocSummaries = store.DocSummaries
		return nil
	}
	// Fallback: legacy format (just chunks array)
	return json.Unmarshal(data, &idx.Chunks)
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

	url := fmt.Sprintf("https://router.huggingface.co/hf-inference/models/%s", e.model)
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
