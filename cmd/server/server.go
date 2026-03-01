package main

import (
	"container/list"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"sync"

	"gocognigo/internal/chat"
	"gocognigo/internal/crypto"
	"gocognigo/internal/indexer"
	"gocognigo/internal/llm"
	"gocognigo/internal/retriever"

	"context"
)

// Server holds all shared state.
type Server struct {
	mu              sync.RWMutex
	activeProjectID string // which project's index is currently loaded
	activeIndex     *indexer.Index
	activeRetriever *retriever.Retriever
	indexLoading    bool // true while background index load is in progress

	// Index cache: LRU cache of loaded indexes keyed by project ID.
	// Keeps up to maxCacheSize entries; least-recently-used is evicted.
	indexCache *lruCache

	projects     *chat.ProjectStore
	ingestStatus *IngestStatus
	ingestCancel context.CancelFunc // cancels the active ingestion goroutine

	providerKeys  map[string]string
	defaultLLM    string
	embedProvider string
	embedAPIKey   string
	ocrProvider   string // "tesseract", "sarvam", or ""
	sarvamAPIKey  string
	tesseractOk   bool // true if tesseract CLI is on PATH
}

const maxCacheSize = 5

type cachedIndex struct {
	idx *indexer.Index
	ret *retriever.Retriever
}

// lruCache is a thread-safe LRU cache for loaded indexes.
type lruCache struct {
	mu      sync.Mutex
	maxSize int
	items   map[string]*list.Element
	order   *list.List // front = most recently used
}

type lruEntry struct {
	key   string
	value *cachedIndex
}

func newLRUCache(maxSize int) *lruCache {
	return &lruCache{
		maxSize: maxSize,
		items:   make(map[string]*list.Element),
		order:   list.New(),
	}
}

// get returns the cached index and true if found, promoting it to front.
func (c *lruCache) get(key string) (*cachedIndex, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[key]; ok {
		c.order.MoveToFront(el)
		return el.Value.(*lruEntry).value, true
	}
	return nil, false
}

// put adds or updates an entry. If the cache is full, the LRU entry is evicted.
func (c *lruCache) put(key string, value *cachedIndex) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[key]; ok {
		c.order.MoveToFront(el)
		el.Value.(*lruEntry).value = value
		return
	}
	// Evict if at capacity
	if c.order.Len() >= c.maxSize {
		oldest := c.order.Back()
		if oldest != nil {
			c.order.Remove(oldest)
			delete(c.items, oldest.Value.(*lruEntry).key)
			log.Printf("LRU cache: evicted index for project %s", oldest.Value.(*lruEntry).key)
		}
	}
	el := c.order.PushFront(&lruEntry{key: key, value: value})
	c.items[key] = el
}

// delete removes an entry from the cache.
func (c *lruCache) delete(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[key]; ok {
		c.order.Remove(el)
		delete(c.items, key)
	}
}

// has returns true if the key is in the cache (without promoting).
func (c *lruCache) has(key string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.items[key]
	return ok
}

// IngestStatus is polled by the frontend to show progress.
type IngestStatus struct {
	mu          sync.RWMutex
	Phase       string       `json:"phase"` // idle, processing, done, error, cancelled
	FilesTotal  int          `json:"files_total"`
	FilesDone   int          `json:"files_done"`
	ChunksTotal int          `json:"chunks_total"`
	ChunksDone  int          `json:"chunks_done"`
	Error       string       `json:"error,omitempty"`
	FileResults []FileResult `json:"file_results,omitempty"`
}

// FileResult tracks per-file processing outcome.
type FileResult struct {
	Name   string `json:"name"`
	Status string `json:"status"` // "ok" or "failed"
	Error  string `json:"error,omitempty"`
	Chunks int    `json:"chunks"`
}

func (s *IngestStatus) snapshot() IngestStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return IngestStatus{
		Phase:       s.Phase,
		FilesTotal:  s.FilesTotal,
		FilesDone:   s.FilesDone,
		ChunksTotal: s.ChunksTotal,
		ChunksDone:  s.ChunksDone,
		Error:       s.Error,
		FileResults: s.FileResults,
	}
}

func (s *IngestStatus) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Phase = "idle"
	s.FilesTotal = 0
	s.FilesDone = 0
	s.ChunksTotal = 0
	s.ChunksDone = 0
	s.Error = ""
	s.FileResults = nil
}

// ----- Request / Response types -----

type QueryRequest struct {
	Question       string `json:"question"`
	Provider       string `json:"provider,omitempty"`
	Model          string `json:"model,omitempty"`
	ProjectID      string `json:"project_id"`
	ConversationID string `json:"conversation_id,omitempty"`
}

type BatchRequest struct {
	Questions []string `json:"questions"`
	Provider  string   `json:"provider,omitempty"`
	Model     string   `json:"model,omitempty"`
	ProjectID string   `json:"project_id"`
}

type BatchResponse struct {
	Answers   []*llm.Answer `json:"answers"`
	TotalTime float64       `json:"total_time_seconds"`
}

type StatsResponse struct {
	Documents  int      `json:"documents"`
	Chunks     int      `json:"chunks"`
	IndexReady bool     `json:"index_ready"`
	Providers  []string `json:"providers"`
	DefaultLLM string   `json:"default_llm"`
}

type ProjectIDRequest struct {
	ProjectID string `json:"chat_id"`
}

// ========== Settings Persistence ==========

const settingsFile = "data/settings.json"

type SavedSettings struct {
	OpenAIKey      string `json:"openai_key"`
	AnthropicKey   string `json:"anthropic_key"`
	HuggingFaceKey string `json:"huggingface_key"`
	DefaultLLM     string `json:"default_llm"`
	EmbedProvider  string `json:"embed_provider"`
	OCRProvider    string `json:"ocr_provider"`
	SarvamKey      string `json:"sarvam_key"`
}

func loadSavedSettings() *SavedSettings {
	data, err := os.ReadFile(settingsFile)
	if err != nil {
		return nil
	}
	var s SavedSettings
	if err := json.Unmarshal(data, &s); err != nil {
		log.Printf("Warning: could not parse %s: %v", settingsFile, err)
		return nil
	}

	// Decrypt API key fields (backward-compatible: if decryption fails, use raw value)
	s.OpenAIKey = decryptOrPassthrough(s.OpenAIKey)
	s.AnthropicKey = decryptOrPassthrough(s.AnthropicKey)
	s.HuggingFaceKey = decryptOrPassthrough(s.HuggingFaceKey)
	s.SarvamKey = decryptOrPassthrough(s.SarvamKey)

	return &s
}

// decryptOrPassthrough tries to decrypt a value; if it fails (e.g. legacy
// plaintext), returns the original value unchanged. This provides backward
// compatibility with existing unencrypted settings.json files.
func decryptOrPassthrough(val string) string {
	if val == "" {
		return ""
	}
	decrypted, err := crypto.Decrypt(val)
	if err != nil {
		// Not encrypted (legacy plaintext) — use as-is
		return val
	}
	return decrypted
}

func persistSettings(s SavedSettings) error {
	_ = os.MkdirAll("data", 0755)

	// Encrypt API key fields before writing to disk
	toSave := s
	var err error
	if toSave.OpenAIKey, err = crypto.Encrypt(s.OpenAIKey); err != nil {
		log.Printf("Warning: failed to encrypt OpenAI key: %v", err)
		toSave.OpenAIKey = s.OpenAIKey // fall back to plaintext
	}
	if toSave.AnthropicKey, err = crypto.Encrypt(s.AnthropicKey); err != nil {
		log.Printf("Warning: failed to encrypt Anthropic key: %v", err)
		toSave.AnthropicKey = s.AnthropicKey
	}
	if toSave.HuggingFaceKey, err = crypto.Encrypt(s.HuggingFaceKey); err != nil {
		log.Printf("Warning: failed to encrypt HuggingFace key: %v", err)
		toSave.HuggingFaceKey = s.HuggingFaceKey
	}
	if toSave.SarvamKey, err = crypto.Encrypt(s.SarvamKey); err != nil {
		log.Printf("Warning: failed to encrypt Sarvam key: %v", err)
		toSave.SarvamKey = s.SarvamKey
	}

	data, err := json.MarshalIndent(toSave, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(settingsFile, data, 0644)
}

func maskKey(key string) string {
	if len(key) <= 8 {
		if key == "" {
			return ""
		}
		return "****"
	}
	return key[:4] + "..." + key[len(key)-4:]
}

// ========== Middleware ==========

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ========== Helpers ==========

func (s *Server) getProvider(requestedProvider, requestedModel string) (llm.Provider, error) {
	provider := requestedProvider
	if provider == "" {
		provider = s.defaultLLM
	}
	apiKey := s.providerKeys[provider]
	if apiKey == "" || apiKey == "your_openai_key_here" || apiKey == "your_anthropic_key_here" {
		return nil, fmt.Errorf("no API key configured for provider: %s", provider)
	}
	return llm.NewProvider(provider, apiKey, requestedModel)
}

func jsonResp(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func jsonErr(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
