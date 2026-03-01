package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"gocognigo/internal/chat"
	"gocognigo/internal/extractor"
	"gocognigo/internal/indexer"
	"gocognigo/internal/llm"
	"gocognigo/internal/retriever"

	"github.com/joho/godotenv"
)

// Server holds all shared state.
type Server struct {
	mu                 sync.RWMutex
	activeProject      *chat.Project
	activeConversation *chat.Conversation
	activeIndex        *indexer.Index
	activeRetriever    *retriever.Retriever
	indexLoading       bool // true while background index load is in progress

	// Index cache: keeps loaded indexes in memory keyed by project ID
	// so switching back to a previously-loaded project is instant.
	indexCache map[string]*cachedIndex

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

type cachedIndex struct {
	idx *indexer.Index
	ret *retriever.Retriever
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
	Question string `json:"question"`
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
}

type BatchRequest struct {
	Questions []string `json:"questions"`
	Provider  string   `json:"provider,omitempty"`
	Model     string   `json:"model,omitempty"`
}

type BatchResponse struct {
	Answers   []*llm.Answer `json:"answers"`
	TotalTime float64       `json:"total_time_seconds"`
}

type StatsResponse struct {
	Documents       int      `json:"documents"`
	Chunks          int      `json:"chunks"`
	IndexReady      bool     `json:"index_ready"`
	Providers       []string `json:"providers"`
	DefaultLLM      string   `json:"default_llm"`
	ActiveProjectID string   `json:"active_chat_id,omitempty"`
	activeProject   string   `json:"active_chat_name,omitempty"`
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
	return &s
}

func persistSettings(s SavedSettings) error {
	_ = os.MkdirAll("data", 0755)
	data, err := json.MarshalIndent(s, "", "  ")
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

// ========== main ==========

func main() {
	_ = godotenv.Load()

	embedProvider := os.Getenv("EMBEDDING_PROVIDER")
	embedAPIKey := os.Getenv("EMBEDDING_API_KEY")
	if embedAPIKey == "" {
		embedAPIKey = os.Getenv("OPENAI_API_KEY")
	}

	providerKeys := map[string]string{
		"openai":      os.Getenv("OPENAI_API_KEY"),
		"huggingface": os.Getenv("HUGGINGFACE_API_KEY"),
		"anthropic":   os.Getenv("ANTHROPIC_API_KEY"),
	}

	defaultLLM := os.Getenv("LLM_PROVIDER")
	if defaultLLM == "" {
		defaultLLM = "openai"
	}

	// Override with saved settings if they exist
	if saved := loadSavedSettings(); saved != nil {
		log.Printf("Loading saved settings from %s", settingsFile)
		if saved.OpenAIKey != "" {
			providerKeys["openai"] = saved.OpenAIKey
		}
		if saved.AnthropicKey != "" {
			providerKeys["anthropic"] = saved.AnthropicKey
		}
		if saved.HuggingFaceKey != "" {
			providerKeys["huggingface"] = saved.HuggingFaceKey
		}
		if saved.DefaultLLM != "" {
			defaultLLM = saved.DefaultLLM
		}
		if saved.EmbedProvider != "" {
			embedProvider = saved.EmbedProvider
			// Resolve embed API key from the chosen provider
			switch saved.EmbedProvider {
			case "openai":
				embedAPIKey = providerKeys["openai"]
			case "huggingface":
				embedAPIKey = providerKeys["huggingface"]
			}
		}
	}

	// OCR configuration
	ocrProvider := os.Getenv("OCR_PROVIDER") // "tesseract", "sarvam", or ""
	sarvamAPIKey := os.Getenv("SARVAM_API_KEY")
	if saved := loadSavedSettings(); saved != nil {
		if saved.OCRProvider != "" {
			ocrProvider = saved.OCRProvider
		}
		if saved.SarvamKey != "" {
			sarvamAPIKey = saved.SarvamKey
		}
	}
	tesseractOk := extractor.DetectTesseract()

	// Smart OCR provider auto-detection when no explicit provider is set
	if ocrProvider == "" {
		if sarvamAPIKey != "" {
			ocrProvider = "sarvam"
			log.Printf("OCR: auto-selected Sarvam (API key configured)")
		} else if tesseractOk {
			ocrProvider = "tesseract"
			log.Printf("OCR: auto-selected Tesseract (detected on system)")
		}
	}

	// Log OCR capability summary
	switch {
	case ocrProvider == "sarvam" && sarvamAPIKey != "":
		log.Printf("OCR ready: Sarvam Document Intelligence (primary), Tesseract=%v (fallback)", tesseractOk)
	case ocrProvider == "tesseract" && tesseractOk:
		hasPdftoppm := extractor.DetectPdftoppm()
		if hasPdftoppm {
			log.Printf("OCR ready: Tesseract + Poppler (primary), Sarvam=%v (fallback)", sarvamAPIKey != "")
		} else {
			log.Printf("OCR WARNING: Tesseract found but Poppler (pdftoppm) is missing — cannot convert PDFs to images")
			log.Printf("  Install Poppler or switch to Sarvam OCR (set SARVAM_API_KEY in .env)")
			if sarvamAPIKey != "" {
				ocrProvider = "sarvam"
				log.Printf("  Auto-switching to Sarvam since API key is available")
			}
		}
	case ocrProvider == "tesseract" && !tesseractOk:
		log.Printf("OCR WARNING: OCR_PROVIDER=tesseract but Tesseract not found")
		if sarvamAPIKey != "" {
			ocrProvider = "sarvam"
			log.Printf("  Auto-switching to Sarvam since API key is available")
		}
	default:
		log.Printf("OCR: no provider configured (scanned PDFs will not be processed)")
		log.Printf("  Set SARVAM_API_KEY in .env for cloud OCR, or install Tesseract + Poppler for local OCR")
	}

	projects, err := chat.NewProjectStore("data/projects")
	if err != nil {
		log.Fatalf("Failed to init project store: %v", err)
	}

	srv := &Server{
		projects:      projects,
		ingestStatus:  &IngestStatus{Phase: "idle"},
		providerKeys:  providerKeys,
		defaultLLM:    defaultLLM,
		embedProvider: embedProvider,
		embedAPIKey:   embedAPIKey,
		ocrProvider:   ocrProvider,
		sarvamAPIKey:  sarvamAPIKey,
		tesseractOk:   tesseractOk,
		indexCache:    make(map[string]*cachedIndex),
	}

	mux := http.NewServeMux()

	// Existing API endpoints
	mux.HandleFunc("/api/query", srv.handleQuery)
	mux.HandleFunc("/api/batch", srv.handleBatch)
	mux.HandleFunc("/api/stats", srv.handleStats)
	mux.HandleFunc("/api/providers", srv.handleProviders)

	// Upload & ingestion endpoints
	mux.HandleFunc("/api/upload", srv.handleUpload)
	mux.HandleFunc("/api/ingest", srv.handleIngest)
	mux.HandleFunc("/api/ingest/status", srv.handleIngestStatus)
	mux.HandleFunc("/api/files", srv.handleFiles)
	mux.HandleFunc("/api/ingest/cancel", srv.handleCancelIngest)
	mux.HandleFunc("/api/files/delete", srv.handleDeleteSingleFile)
	mux.HandleFunc("/api/settings", srv.handleSettings)
	mux.HandleFunc("/api/index-status", srv.handleIndexStatus)

	// Project endpoints
	mux.HandleFunc("/api/chats", srv.handleProjects)
	mux.HandleFunc("/api/chats/activate", srv.handleActivateProject)
	mux.HandleFunc("/api/chats/delete", srv.handleDeleteProject)
	mux.HandleFunc("/api/chats/rename", srv.handleRenameProject)

	// Conversation endpoints
	mux.HandleFunc("/api/conversations", srv.handleConversations)
	mux.HandleFunc("/api/conversations/delete", srv.handleDeleteConversation)
	mux.HandleFunc("/api/conversations/messages", srv.handleMessages)
	mux.HandleFunc("/api/conversations/rename", srv.handleRenameConversation)

	// Static files
	mux.Handle("/", http.FileServer(http.Dir("web")))

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	log.Printf("GoCognigo server starting on http://localhost:%s", port)
	if err := http.ListenAndServe(":"+port, corsMiddleware(mux)); err != nil {
		log.Fatal(err)
	}
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

// ========== Helper ==========

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

// ========== Project Endpoints ==========

func (s *Server) handleProjects(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		jsonResp(w, s.projects.List())
	case http.MethodPost:
		var req struct {
			Name string `json:"name"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		sess, err := s.projects.Create(req.Name)
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		// Auto-activate the new chat
		s.mu.Lock()
		// Close previous index if any
		if s.activeIndex != nil {
			_ = s.activeIndex.Close()
			s.activeIndex = nil
			s.activeRetriever = nil
		}
		s.activeProject = sess
		s.ingestStatus.reset()
		s.mu.Unlock()
		jsonResp(w, sess)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleActivateProject(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req ProjectIDRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ProjectID == "" {
		jsonErr(w, "chat_id is required", http.StatusBadRequest)
		return
	}

	sess, err := s.projects.Get(req.ProjectID)
	if err != nil {
		jsonErr(w, err.Error(), http.StatusNotFound)
		return
	}

	s.mu.Lock()
	// Don't close previous index — it stays in cache
	s.activeIndex = nil
	s.activeRetriever = nil
	s.activeProject = sess
	s.activeConversation = nil // reset conversation
	s.ingestStatus.reset()
	s.indexLoading = false
	s.mu.Unlock()

	// If the project is ready, check cache first then load in background
	if sess.Status == "ready" {
		// Set up conversations immediately (fast, no disk I/O)
		convs := s.projects.ListConversations(sess.ID)
		if len(convs) == 0 {
			conv, _ := s.projects.CreateConversation(sess.ID, "")
			if conv != nil {
				s.mu.Lock()
				s.activeConversation = conv
				s.mu.Unlock()
			}
		} else {
			last := convs[len(convs)-1]
			s.mu.Lock()
			s.activeConversation = &last
			s.mu.Unlock()
		}

		// Check in-memory cache first — instant if already loaded
		s.mu.Lock()
		if cached, ok := s.indexCache[sess.ID]; ok {
			s.activeIndex = cached.idx
			s.activeRetriever = cached.ret
			s.mu.Unlock()
			log.Printf("Index cache hit for project %s — instant switch", sess.ID)
		} else {
			// Signal that index is loading
			s.indexLoading = true
			s.mu.Unlock()
			// Load in background
			go func(projectID string) {
				if err := s.loadChatIndexes(projectID); err != nil {
					log.Printf("Warning: could not load indexes for project %s: %v", projectID, err)
				}
				s.mu.Lock()
				s.indexLoading = false
				s.mu.Unlock()
			}(sess.ID)
		}
	}

	jsonResp(w, sess)
}

func (s *Server) handleDeleteProject(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete && r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req ProjectIDRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ProjectID == "" {
		jsonErr(w, "chat_id is required", http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	// If deleting the active project, cancel any active ingestion and clear state
	if s.activeProject != nil && s.activeProject.ID == req.ProjectID {
		if s.ingestCancel != nil {
			s.ingestCancel()
			s.ingestCancel = nil
		}
		if s.activeIndex != nil {
			_ = s.activeIndex.Close()
			s.activeIndex = nil
			s.activeRetriever = nil
		}
		s.activeProject = nil
		s.activeConversation = nil
		s.ingestStatus.reset()
	}
	s.mu.Unlock()

	if err := s.projects.Delete(req.ProjectID); err != nil {
		jsonErr(w, err.Error(), http.StatusNotFound)
		return
	}

	jsonResp(w, map[string]string{"status": "deleted"})
}

func (s *Server) handleRenameProject(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		ProjectID string `json:"chat_id"`
		Name      string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ProjectID == "" || req.Name == "" {
		jsonErr(w, "chat_id and name are required", http.StatusBadRequest)
		return
	}

	sess, err := s.projects.Get(req.ProjectID)
	if err != nil {
		jsonErr(w, err.Error(), http.StatusNotFound)
		return
	}

	sess.Name = req.Name
	if err := s.projects.Update(*sess); err != nil {
		jsonErr(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Update active chat if it's the same
	s.mu.Lock()
	if s.activeProject != nil && s.activeProject.ID == req.ProjectID {
		s.activeProject.Name = req.Name
	}
	s.mu.Unlock()

	jsonResp(w, sess)
}

// ========== File Upload & Ingestion Endpoints ==========

func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	s.mu.RLock()
	activeProject := s.activeProject
	s.mu.RUnlock()

	if activeProject == nil {
		jsonErr(w, "No active Project. Create or activate a chat first.", http.StatusBadRequest)
		return
	}

	// Parse multipart (max 100MB)
	if err := r.ParseMultipartForm(100 << 20); err != nil {
		jsonErr(w, "Failed to parse upload: "+err.Error(), http.StatusBadRequest)
		return
	}

	files := r.MultipartForm.File["files"]
	if len(files) == 0 {
		// Try singular "file" field
		files = r.MultipartForm.File["file"]
	}
	if len(files) == 0 {
		jsonErr(w, "No files uploaded", http.StatusBadRequest)
		return
	}

	uploadsDir := s.projects.UploadsDir(activeProject.ID)
	_ = os.MkdirAll(uploadsDir, 0755)

	var saved []string
	for _, fh := range files {
		// Only allow PDF and DOCX
		ext := strings.ToLower(filepath.Ext(fh.Filename))
		if ext != ".pdf" && ext != ".docx" {
			continue
		}

		src, err := fh.Open()
		if err != nil {
			continue
		}

		dstPath := filepath.Join(uploadsDir, fh.Filename)
		dst, err := os.Create(dstPath)
		if err != nil {
			src.Close()
			continue
		}
		_, _ = io.Copy(dst, src)
		src.Close()
		dst.Close()
		saved = append(saved, fh.Filename)
	}

	// Update session file count
	dirEntries, _ := os.ReadDir(uploadsDir)
	fileCount := 0
	for _, e := range dirEntries {
		if !e.IsDir() {
			fileCount++
		}
	}
	sess, _ := s.projects.Get(activeProject.ID)
	if sess != nil {
		sess.FileCount = fileCount
		_ = s.projects.Update(*sess)
		s.mu.Lock()
		s.activeProject = sess
		s.mu.Unlock()
	}

	jsonResp(w, map[string]interface{}{
		"uploaded": saved,
		"count":    len(saved),
	})
}

func (s *Server) handleFiles(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	activeProject := s.activeProject
	s.mu.RUnlock()

	if activeProject == nil {
		jsonErr(w, "No active Project", http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodGet:
		uploadsDir := s.projects.UploadsDir(activeProject.ID)
		entries, _ := os.ReadDir(uploadsDir)
		var files []map[string]interface{}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			info, _ := e.Info()
			size := int64(0)
			if info != nil {
				size = info.Size()
			}
			files = append(files, map[string]interface{}{
				"name": e.Name(),
				"size": size,
			})
		}
		if files == nil {
			files = []map[string]interface{}{}
		}
		jsonResp(w, files)

	case http.MethodDelete:
		// Clear uploads and indexes for this chat
		uploadsDir := s.projects.UploadsDir(activeProject.ID)
		bm25Dir := s.projects.BM25Dir(activeProject.ID)
		vectorsPath := s.projects.VectorsPath(activeProject.ID)

		s.mu.Lock()
		if s.activeIndex != nil {
			_ = s.activeIndex.Close()
			s.activeIndex = nil
			s.activeRetriever = nil
		}
		s.mu.Unlock()

		_ = os.RemoveAll(uploadsDir)
		_ = os.MkdirAll(uploadsDir, 0755)
		_ = os.RemoveAll(bm25Dir)
		_ = os.Remove(vectorsPath)

		sess, _ := s.projects.Get(activeProject.ID)
		if sess != nil {
			sess.FileCount = 0
			sess.ChunkCount = 0
			sess.Status = "upload"
			_ = s.projects.Update(*sess)
			s.mu.Lock()
			s.activeProject = sess
			s.mu.Unlock()
		}

		s.ingestStatus.reset()
		jsonResp(w, map[string]string{"status": "cleared"})

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleDeleteSingleFile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	s.mu.RLock()
	activeProject := s.activeProject
	s.mu.RUnlock()

	if activeProject == nil {
		jsonErr(w, "No active Project", http.StatusBadRequest)
		return
	}

	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		jsonErr(w, "name is required", http.StatusBadRequest)
		return
	}

	// Prevent path traversal
	clean := filepath.Base(req.Name)
	if clean != req.Name || clean == "." || clean == ".." {
		jsonErr(w, "invalid filename", http.StatusBadRequest)
		return
	}

	uploadsDir := s.projects.UploadsDir(activeProject.ID)
	targetPath := filepath.Join(uploadsDir, clean)

	if _, err := os.Stat(targetPath); os.IsNotExist(err) {
		jsonErr(w, "file not found", http.StatusNotFound)
		return
	}

	if err := os.Remove(targetPath); err != nil {
		jsonErr(w, "failed to delete file: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Update file count
	entries, _ := os.ReadDir(uploadsDir)
	fileCount := 0
	for _, e := range entries {
		if !e.IsDir() {
			fileCount++
		}
	}
	sess, _ := s.projects.Get(activeProject.ID)
	if sess != nil {
		sess.FileCount = fileCount
		_ = s.projects.Update(*sess)
		s.mu.Lock()
		s.activeProject = sess
		s.mu.Unlock()
	}

	jsonResp(w, map[string]interface{}{"status": "deleted", "remaining": fileCount})
}

func (s *Server) handleIngest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	s.mu.RLock()
	activeProject := s.activeProject
	s.mu.RUnlock()

	if activeProject == nil {
		jsonErr(w, "No active Project", http.StatusBadRequest)
		return
	}

	// Don't start if already running
	snap := s.ingestStatus.snapshot()
	if snap.Phase == "processing" {
		jsonErr(w, "Ingestion already in progress", http.StatusConflict)
		return
	}

	ProjectID := activeProject.ID
	uploadsDir := s.projects.UploadsDir(ProjectID)
	bm25Dir := s.projects.BM25Dir(ProjectID)
	vectorsPath := s.projects.VectorsPath(ProjectID)

	// Gather files
	entries, _ := os.ReadDir(uploadsDir)
	var uploadedFiles []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(e.Name()))
		if ext == ".pdf" || ext == ".docx" {
			uploadedFiles = append(uploadedFiles, e.Name())
		}
	}

	if len(uploadedFiles) == 0 {
		jsonErr(w, "No files to process", http.StatusBadRequest)
		return
	}

	// Update session status
	sess, _ := s.projects.Get(ProjectID)
	if sess != nil {
		sess.Status = "processing"
		_ = s.projects.Update(*sess)
	}

	// Reset ingest status
	s.ingestStatus.mu.Lock()
	s.ingestStatus.Phase = "processing"
	s.ingestStatus.FilesTotal = len(uploadedFiles)
	s.ingestStatus.FilesDone = 0
	s.ingestStatus.ChunksTotal = 0
	s.ingestStatus.ChunksDone = 0
	s.ingestStatus.Error = ""
	s.ingestStatus.mu.Unlock()

	// Create cancellable context for this ingestion run
	ctx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	s.ingestCancel = cancel
	s.mu.Unlock()

	// Run ingestion in background
	go s.runIngestion(ctx, ProjectID, uploadsDir, bm25Dir, vectorsPath, uploadedFiles)

	jsonResp(w, map[string]string{"status": "started"})
}

func (s *Server) runIngestion(ctx context.Context, ProjectID, uploadsDir, bm25Dir, vectorsPath string, files []string) {
	// Clear cancel func when done
	defer func() {
		s.mu.Lock()
		s.ingestCancel = nil
		s.mu.Unlock()
	}()

	// Close old index if loaded for this chat
	s.mu.Lock()
	if s.activeIndex != nil {
		_ = s.activeIndex.Close()
		s.activeIndex = nil
		s.activeRetriever = nil
	}
	s.mu.Unlock()

	// Remove old BM25 index directory so bleve can create a fresh one
	_ = os.RemoveAll(bm25Dir)

	// Create fresh index
	idx, err := indexer.NewIndex(s.embedProvider, s.embedAPIKey, "", bm25Dir)
	if err != nil {
		s.ingestStatus.mu.Lock()
		s.ingestStatus.Phase = "error"
		s.ingestStatus.Error = fmt.Sprintf("Failed to create index: %v", err)
		s.ingestStatus.mu.Unlock()
		return
	}

	// ===== STREAMED PIPELINE =====
	// Each file flows: Extract → Summary (async) + Chunk → Embed
	// No barrier between extraction and embedding — as soon as a file is
	// extracted, its chunks enter the embedding pipeline immediately.

	type extractResult struct {
		chunks []extractor.DocumentChunk
		err    error
		file   string
	}

	// Thread-safe counters for progress
	var (
		filesDone   int32 // atomic
		chunksTotal int64 // atomic — grows as files are chunked
	)

	// Channel to stream extracted files into the processor
	resultsCh := make(chan extractResult, len(files))
	extractSem := make(chan struct{}, 4) // max 4 concurrent extractions
	var extractWg sync.WaitGroup

	// --- Extraction workers: extract files in parallel ---
	for _, filename := range files {
		extractWg.Add(1)
		go func(fname string) {
			defer extractWg.Done()

			select {
			case <-ctx.Done():
				resultsCh <- extractResult{nil, ctx.Err(), fname}
				return
			case extractSem <- struct{}{}:
			}
			defer func() { <-extractSem }()

			filePath := filepath.Join(uploadsDir, fname)
			ext := strings.ToLower(filepath.Ext(fname))

			start := time.Now()
			log.Printf("Extracting %s...", fname)

			s.mu.RLock()
			ocrCfg := &extractor.OCRConfig{
				Provider:    s.ocrProvider,
				SarvamKey:   s.sarvamAPIKey,
				TesseractOk: s.tesseractOk,
			}
			s.mu.RUnlock()

			var docChunks []extractor.DocumentChunk
			var extractErr error
			switch ext {
			case ".pdf":
				docChunks, extractErr = extractor.ExtractPDF(filePath, ocrCfg)
			case ".docx":
				docChunks, extractErr = extractor.ExtractDOCX(filePath)
			}

			elapsed := time.Since(start)
			if extractErr != nil {
				log.Printf("Failed to extract %s after %v: %v", fname, elapsed, extractErr)
				resultsCh <- extractResult{nil, extractErr, fname}
			} else {
				log.Printf("Extracted %s: %d pages in %v", fname, len(docChunks), elapsed)
				resultsCh <- extractResult{docChunks, nil, fname}
			}

			newDone := int(atomic.AddInt32(&filesDone, 1))
			s.ingestStatus.mu.Lock()
			s.ingestStatus.FilesDone = newDone
			s.ingestStatus.mu.Unlock()
		}(filename)
	}

	// Close resultsCh when all extractions finish
	go func() {
		extractWg.Wait()
		close(resultsCh)
	}()

	// --- Processor: reads extraction results, chunks, and submits to embedding ---
	// The embedding work is done concurrently via idx.EmbedAndIndex which
	// internally uses a 6-concurrent-batch worker pool.

	s.mu.RLock()
	openAIKey := s.providerKeys["openai"]
	s.mu.RUnlock()

	var fileResults []FileResult
	var fileResultsMu sync.Mutex
	var embedWg sync.WaitGroup   // tracks in-flight embedding goroutines
	var summaryWg sync.WaitGroup // tracks in-flight summary goroutines

	var firstErr error
	var errOnce sync.Once
	var anyFileOk bool

	for res := range resultsCh {
		if ctx.Err() != nil {
			break
		}

		if res.err != nil || res.chunks == nil {
			errMsg := "unknown error"
			if res.err != nil {
				errMsg = res.err.Error()
			}
			fileResultsMu.Lock()
			fileResults = append(fileResults, FileResult{
				Name:   res.file,
				Status: "failed",
				Error:  errMsg,
			})
			fileResultsMu.Unlock()
			continue
		}

		anyFileOk = true
		docChunks := res.chunks
		fileName := res.file

		// Generate doc summary in the background (non-blocking)
		if openAIKey != "" {
			summaryWg.Add(1)
			go func(dc []extractor.DocumentChunk, fname string) {
				defer summaryWg.Done()
				var pages []string
				for _, c := range dc {
					pages = append(pages, c.Text)
				}
				summary, err := llm.GenerateDocSummary(ctx, openAIKey, fname, pages, len(pages))
				if err != nil {
					log.Printf("Warning: failed to generate summary for %s: %v", fname, err)
					return
				}
				idx.AddDocSummary(*summary)
				log.Printf("Generated summary for %s: %s (%s)", fname, summary.Title, summary.DocType)
			}(docChunks, fileName)
		}

		// Chunk this file's pages immediately
		fileChunks := idx.ChunkPages(docChunks)
		numChunks := len(fileChunks)
		log.Printf("Chunked %s: %d pages → %d chunks", fileName, len(docChunks), numChunks)

		// Update running total of chunks
		atomic.AddInt64(&chunksTotal, int64(numChunks))
		s.ingestStatus.mu.Lock()
		s.ingestStatus.ChunksTotal = int(atomic.LoadInt64(&chunksTotal))
		s.ingestStatus.mu.Unlock()

		fileResultsMu.Lock()
		fileResults = append(fileResults, FileResult{
			Name:   fileName,
			Status: "ok",
			Chunks: numChunks,
		})
		fileResultsMu.Unlock()

		// Submit chunks for embedding (runs concurrently with next file's extraction)
		embedWg.Add(1)
		go func(chunks []indexer.Chunk, fname string) {
			defer embedWg.Done()

			// Progress callback: update global status from actual indexed count
			embedProgress := func(total, done int) {
				s.ingestStatus.mu.Lock()
				s.ingestStatus.ChunksTotal = int(atomic.LoadInt64(&chunksTotal))
				s.ingestStatus.ChunksDone = len(idx.Chunks) // actual embedded count
				s.ingestStatus.mu.Unlock()
			}

			if err := idx.EmbedAndIndex(ctx, chunks, embedProgress, 0); err != nil {
				if ctx.Err() == nil {
					errOnce.Do(func() { firstErr = err })
					log.Printf("Embedding error for %s: %v", fname, err)
				}
			}
		}(fileChunks, fileName)
	}

	// Wait for all embedding work to complete
	embedWg.Wait()
	// Wait for all summary generation to complete
	summaryWg.Wait()

	// Store file results
	s.ingestStatus.mu.Lock()
	s.ingestStatus.FileResults = fileResults
	s.ingestStatus.mu.Unlock()

	// Check for cancellation
	if ctx.Err() != nil {
		log.Printf("Ingestion cancelled")
		s.ingestStatus.mu.Lock()
		s.ingestStatus.Phase = "cancelled"
		s.ingestStatus.Error = "Processing was cancelled"
		s.ingestStatus.mu.Unlock()
		_ = idx.Close()
		return
	}

	// Check if any files succeeded
	if !anyFileOk {
		log.Printf("No text extracted from any uploaded file")
		s.ingestStatus.mu.Lock()
		s.ingestStatus.Phase = "error"
		s.ingestStatus.Error = "No text could be extracted from any uploaded file. If your PDFs are scanned images, configure an OCR provider in Settings (Tesseract or Sarvam Vision)."
		s.ingestStatus.mu.Unlock()
		_ = idx.Close()
		return
	}

	// Check for embedding errors
	if firstErr != nil {
		s.ingestStatus.mu.Lock()
		s.ingestStatus.Phase = "error"
		s.ingestStatus.Error = fmt.Sprintf("Embedding error: %v", firstErr)
		s.ingestStatus.mu.Unlock()
		_ = idx.Close()
		return
	}

	log.Printf("All files processed: %d chunks total", len(idx.Chunks))

	// Save vectors
	if err := idx.SaveVectors(vectorsPath); err != nil {
		log.Printf("Failed to save vectors: %v", err)
	}

	// Update ingest status
	s.ingestStatus.mu.Lock()
	s.ingestStatus.Phase = "done"
	s.ingestStatus.ChunksDone = len(idx.Chunks)
	s.ingestStatus.ChunksTotal = len(idx.Chunks)
	s.ingestStatus.mu.Unlock()

	// Set active retriever
	s.mu.Lock()
	s.activeIndex = idx
	s.activeRetriever = retriever.NewRetriever(idx)
	s.mu.Unlock()

	// Update session
	sess, _ := s.projects.Get(ProjectID)
	if sess != nil {
		sess.Status = "ready"
		sess.ChunkCount = len(idx.Chunks)
		_ = s.projects.Update(*sess)
		s.mu.Lock()
		s.activeProject = sess
		s.mu.Unlock()
	}

	log.Printf("Ingestion complete for chat %s: %d chunks", ProjectID, len(idx.Chunks))
}

func (s *Server) handleIngestStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	snap := s.ingestStatus.snapshot()
	jsonResp(w, snap)
}

func (s *Server) handleCancelIngest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	s.mu.Lock()
	cancel := s.ingestCancel
	s.mu.Unlock()

	if cancel != nil {
		cancel()
		log.Printf("Ingestion cancel requested by user")
	}

	// Always reset session status back to upload (handles stale "processing" state too)
	s.mu.RLock()
	activeProject := s.activeProject
	s.mu.RUnlock()
	if activeProject != nil {
		sess, _ := s.projects.Get(activeProject.ID)
		if sess != nil && (sess.Status == "processing" || sess.Status == "upload") {
			sess.Status = "upload"
			_ = s.projects.Update(*sess)
		}
	}

	// Reset ingest status to idle
	s.ingestStatus.mu.Lock()
	s.ingestStatus.Phase = "idle"
	s.ingestStatus.Error = ""
	s.ingestStatus.mu.Unlock()

	jsonResp(w, map[string]string{"status": "cancelled"})
}

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
		// Only update keys if a real (non-masked) value was sent
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

		// Update OCR settings
		s.ocrProvider = req.OCRProvider
		if req.SarvamKey != "" && !strings.Contains(req.SarvamKey, "...") {
			s.sarvamAPIKey = req.SarvamKey
		}

		// Build settings to persist (use actual keys, not masked)
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

// handleIndexStatus returns whether the vector index is loaded and ready for queries.
func (s *Server) handleIndexStatus(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	ready := s.activeRetriever != nil
	loading := s.indexLoading
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

// loadChatIndexes loads a chat's pre-built indexes from disk.
func (s *Server) loadChatIndexes(ProjectID string) error {
	bm25Dir := s.projects.BM25Dir(ProjectID)
	vectorsPath := s.projects.VectorsPath(ProjectID)

	// Check if vectors file exists
	if _, err := os.Stat(vectorsPath); os.IsNotExist(err) {
		return fmt.Errorf("no vectors file for chat %s", ProjectID)
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
	// Store in cache for future instant switches
	if s.indexCache == nil {
		s.indexCache = make(map[string]*cachedIndex)
	}
	s.indexCache[ProjectID] = &cachedIndex{idx: idx, ret: ret}
	s.mu.Unlock()

	log.Printf("Loaded %d chunks for project %s (cached)", len(idx.Chunks), ProjectID)
	return nil
}

// ========== Query Endpoints ==========

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

	s.mu.RLock()
	ret := s.activeRetriever
	s.mu.RUnlock()

	if ret == nil {
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

	results, err := ret.Search(ctx, req.Question, 20)
	if err != nil {
		jsonErr(w, fmt.Sprintf("Retrieval error: %v", err), http.StatusInternalServerError)
		return
	}

	answer, err := llmClient.AnswerQuestion(ctx, req.Question, results, ret.DocSummaries)
	if err != nil {
		jsonErr(w, fmt.Sprintf("LLM error: %v", err), http.StatusInternalServerError)
		return
	}

	elapsed := time.Since(start).Seconds()

	// Persist messages to conversation if one is active
	s.mu.RLock()
	activeConv := s.activeConversation
	activeProj := s.activeProject
	s.mu.RUnlock()

	if activeConv != nil && activeProj != nil {
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
			_ = s.projects.SaveMessage(activeProj.ID, activeConv.ID, userMsg)
			_ = s.projects.SaveMessage(activeProj.ID, activeConv.ID, assistantMsg)
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

	s.mu.RLock()
	ret := s.activeRetriever
	s.mu.RUnlock()

	if ret == nil {
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
			results, err := ret.Search(ctx, question, 20)
			if err != nil {
				mu.Lock()
				errors = append(errors, fmt.Sprintf("Q%d retrieval: %v", idx, err))
				mu.Unlock()
				return
			}
			answer, err := llmClient.AnswerQuestion(ctx, question, results, ret.DocSummaries)
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
	s.mu.RLock()
	activeProject := s.activeProject
	idx := s.activeIndex
	s.mu.RUnlock()

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
	if activeProject != nil {
		resp.ActiveProjectID = activeProject.ID
		resp.activeProject = activeProject.Name
	}

	jsonResp(w, resp)
}

func (s *Server) handleProviders(w http.ResponseWriter, r *http.Request) {
	allModels := map[string][]map[string]string{
		"openai": {
			{"id": "gpt-5.2", "name": "GPT-5.2 (Flagship)"},
			{"id": "gpt-5.2-pro", "name": "GPT-5.2 Pro"},
			{"id": "gpt-5-mini", "name": "GPT-5 Mini"},
			{"id": "gpt-5-nano", "name": "GPT-5 Nano"},
			{"id": "gpt-4.1", "name": "GPT-4.1"},
			{"id": "gpt-4.1-mini", "name": "GPT-4.1 Mini"},
			{"id": "gpt-4.1-nano", "name": "GPT-4.1 Nano"},
			{"id": "gpt-4o", "name": "GPT-4o"},
			{"id": "gpt-4o-mini", "name": "GPT-4o Mini"},
			{"id": "o3-mini", "name": "o3-mini"},
			{"id": "gpt-3.5-turbo", "name": "GPT-3.5 Turbo"},
		},
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
			{"id": "mistralai/Mistral-7B-Instruct-v0.3", "name": "Mistral 7B Instruct"},
			{"id": "meta-llama/Meta-Llama-3-8B-Instruct", "name": "Llama 3 8B Instruct"},
			{"id": "Qwen/Qwen2.5-72B-Instruct", "name": "Qwen 2.5 72B"},
			{"id": "microsoft/Phi-3-mini-4k-instruct", "name": "Phi-3 Mini"},
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

// ========== Conversation Endpoints ==========

func (s *Server) handleConversations(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	proj := s.activeProject
	s.mu.RUnlock()

	if proj == nil {
		jsonErr(w, "No active project", http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodGet:
		convs := s.projects.ListConversations(proj.ID)
		if convs == nil {
			convs = []chat.Conversation{}
		}

		// Also include active conversation ID
		s.mu.RLock()
		activeConvID := ""
		if s.activeConversation != nil {
			activeConvID = s.activeConversation.ID
		}
		s.mu.RUnlock()

		jsonResp(w, map[string]interface{}{
			"conversations":       convs,
			"active_conversation": activeConvID,
		})

	case http.MethodPost:
		var req struct {
			Name string `json:"name"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)

		conv, err := s.projects.CreateConversation(proj.ID, req.Name)
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// Auto-activate the new conversation
		s.mu.Lock()
		s.activeConversation = conv
		s.mu.Unlock()

		jsonResp(w, conv)

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleDeleteConversation(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		ConversationID string `json:"conversation_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonErr(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	s.mu.RLock()
	proj := s.activeProject
	s.mu.RUnlock()

	if proj == nil {
		jsonErr(w, "No active project", http.StatusBadRequest)
		return
	}

	// If deleting the active conversation, clear it
	s.mu.Lock()
	if s.activeConversation != nil && s.activeConversation.ID == req.ConversationID {
		s.activeConversation = nil
	}
	s.mu.Unlock()

	_ = s.projects.DeleteConversation(proj.ID, req.ConversationID)
	jsonResp(w, map[string]string{"status": "ok"})
}

func (s *Server) handleMessages(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		ConversationID string `json:"conversation_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonErr(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	s.mu.RLock()
	proj := s.activeProject
	s.mu.RUnlock()

	if proj == nil {
		jsonErr(w, "No active project", http.StatusBadRequest)
		return
	}

	// Set this as the active conversation
	conv, err := s.projects.GetConversation(proj.ID, req.ConversationID)
	if err != nil {
		jsonErr(w, err.Error(), http.StatusNotFound)
		return
	}

	s.mu.Lock()
	s.activeConversation = conv
	s.mu.Unlock()

	msgs, err := s.projects.LoadMessages(proj.ID, req.ConversationID)
	if err != nil {
		msgs = []chat.Message{}
	}

	jsonResp(w, msgs)
}

func (s *Server) handleRenameConversation(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		ConversationID string `json:"conversation_id"`
		Name           string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonErr(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	s.mu.RLock()
	proj := s.activeProject
	s.mu.RUnlock()

	if proj == nil {
		jsonErr(w, "No active project", http.StatusBadRequest)
		return
	}

	conv, err := s.projects.GetConversation(proj.ID, req.ConversationID)
	if err != nil {
		jsonErr(w, err.Error(), http.StatusNotFound)
		return
	}

	conv.Name = req.Name
	if err := s.projects.UpdateConversation(*conv); err != nil {
		jsonErr(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Update active conversation name if matching
	s.mu.Lock()
	if s.activeConversation != nil && s.activeConversation.ID == req.ConversationID {
		s.activeConversation.Name = req.Name
	}
	s.mu.Unlock()

	jsonResp(w, conv)
}
