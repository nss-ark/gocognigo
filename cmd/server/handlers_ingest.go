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

	"gocognigo/internal/extractor"
	"gocognigo/internal/indexer"
	"gocognigo/internal/llm"
	"gocognigo/internal/retriever"
)

// ========== File Upload & Ingestion Endpoints ==========

func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse multipart (max 100MB)
	if err := r.ParseMultipartForm(100 << 20); err != nil {
		jsonErr(w, "Failed to parse upload: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Get project_id from form field
	projectID := r.FormValue("project_id")
	if projectID == "" {
		jsonErr(w, "project_id is required", http.StatusBadRequest)
		return
	}

	proj, err := s.projects.Get(projectID)
	if err != nil {
		jsonErr(w, "Project not found", http.StatusNotFound)
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

	uploadsDir := s.projects.UploadsDir(projectID)
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
	proj.FileCount = fileCount
	_ = s.projects.Update(*proj)

	jsonResp(w, map[string]interface{}{
		"uploaded": saved,
		"count":    len(saved),
	})
}

func (s *Server) handleFiles(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		projectID := r.URL.Query().Get("project_id")
		if projectID == "" {
			jsonErr(w, "project_id is required", http.StatusBadRequest)
			return
		}

		uploadsDir := s.projects.UploadsDir(projectID)
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
		var req struct {
			ProjectID string `json:"project_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ProjectID == "" {
			jsonErr(w, "project_id is required", http.StatusBadRequest)
			return
		}

		// Clear uploads and indexes for this project
		uploadsDir := s.projects.UploadsDir(req.ProjectID)
		bm25Dir := s.projects.BM25Dir(req.ProjectID)
		vectorsPath := s.projects.VectorsPath(req.ProjectID)

		s.mu.Lock()
		if s.activeProjectID == req.ProjectID && s.activeIndex != nil {
			_ = s.activeIndex.Close()
			s.activeIndex = nil
			s.activeRetriever = nil
		}
		s.indexCache.delete(req.ProjectID)
		s.mu.Unlock()

		_ = os.RemoveAll(uploadsDir)
		_ = os.MkdirAll(uploadsDir, 0755)
		_ = os.RemoveAll(bm25Dir)
		_ = os.Remove(vectorsPath)

		sess, _ := s.projects.Get(req.ProjectID)
		if sess != nil {
			sess.FileCount = 0
			sess.ChunkCount = 0
			sess.Status = "upload"
			_ = s.projects.Update(*sess)
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

	var req struct {
		ProjectID string `json:"project_id"`
		Name      string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" || req.ProjectID == "" {
		jsonErr(w, "project_id and name are required", http.StatusBadRequest)
		return
	}

	// Prevent path traversal
	clean := filepath.Base(req.Name)
	if clean != req.Name || clean == "." || clean == ".." {
		jsonErr(w, "invalid filename", http.StatusBadRequest)
		return
	}

	uploadsDir := s.projects.UploadsDir(req.ProjectID)
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
	sess, _ := s.projects.Get(req.ProjectID)
	if sess != nil {
		sess.FileCount = fileCount
		_ = s.projects.Update(*sess)
	}

	jsonResp(w, map[string]interface{}{"status": "deleted", "remaining": fileCount})
}

func (s *Server) handleIngest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		ProjectID string `json:"project_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ProjectID == "" {
		jsonErr(w, "project_id is required", http.StatusBadRequest)
		return
	}

	// Don't start if already running
	snap := s.ingestStatus.snapshot()
	if snap.Phase == "processing" {
		jsonErr(w, "Ingestion already in progress", http.StatusConflict)
		return
	}

	projectID := req.ProjectID
	uploadsDir := s.projects.UploadsDir(projectID)
	bm25Dir := s.projects.BM25Dir(projectID)
	vectorsPath := s.projects.VectorsPath(projectID)

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
	sess, _ := s.projects.Get(projectID)
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
	s.activeProjectID = projectID
	s.mu.Unlock()

	// Run ingestion in background
	go s.runIngestion(ctx, projectID, uploadsDir, bm25Dir, vectorsPath, uploadedFiles)

	jsonResp(w, map[string]string{"status": "started"})
}

func (s *Server) runIngestion(ctx context.Context, ProjectID, uploadsDir, bm25Dir, vectorsPath string, files []string) {
	// Clear cancel func when done
	defer func() {
		s.mu.Lock()
		s.ingestCancel = nil
		s.mu.Unlock()
	}()

	// Close old index if loaded for this project
	s.mu.Lock()
	if s.activeProjectID == ProjectID && s.activeIndex != nil {
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
	type extractResult struct {
		chunks []extractor.DocumentChunk
		err    error
		file   string
	}

	var (
		filesDone   int32
		chunksTotal int64
	)

	resultsCh := make(chan extractResult, len(files))
	extractSem := make(chan struct{}, 4)
	var extractWg sync.WaitGroup

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

	go func() {
		extractWg.Wait()
		close(resultsCh)
	}()

	s.mu.RLock()
	openAIKey := s.providerKeys["openai"]
	s.mu.RUnlock()

	var fileResults []FileResult
	var fileResultsMu sync.Mutex
	var embedWg sync.WaitGroup
	var summaryWg sync.WaitGroup

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

		fileChunks := idx.ChunkPages(docChunks)
		numChunks := len(fileChunks)
		log.Printf("Chunked %s: %d pages → %d chunks", fileName, len(docChunks), numChunks)

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

		embedWg.Add(1)
		go func(chunks []indexer.Chunk, fname string) {
			defer embedWg.Done()

			embedProgress := func(total, done int) {
				s.ingestStatus.mu.Lock()
				s.ingestStatus.ChunksTotal = int(atomic.LoadInt64(&chunksTotal))
				s.ingestStatus.ChunksDone = len(idx.Chunks)
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

	embedWg.Wait()
	summaryWg.Wait()

	s.ingestStatus.mu.Lock()
	s.ingestStatus.FileResults = fileResults
	s.ingestStatus.mu.Unlock()

	if ctx.Err() != nil {
		log.Printf("Ingestion cancelled")
		s.ingestStatus.mu.Lock()
		s.ingestStatus.Phase = "cancelled"
		s.ingestStatus.Error = "Processing was cancelled"
		s.ingestStatus.mu.Unlock()
		_ = idx.Close()
		return
	}

	if !anyFileOk {
		log.Printf("No text extracted from any uploaded file")
		s.ingestStatus.mu.Lock()
		s.ingestStatus.Phase = "error"
		s.ingestStatus.Error = "No text could be extracted from any uploaded file. If your PDFs are scanned images, configure an OCR provider in Settings (Tesseract or Sarvam Vision)."
		s.ingestStatus.mu.Unlock()
		_ = idx.Close()
		return
	}

	if firstErr != nil {
		s.ingestStatus.mu.Lock()
		s.ingestStatus.Phase = "error"
		s.ingestStatus.Error = fmt.Sprintf("Embedding error: %v", firstErr)
		s.ingestStatus.mu.Unlock()
		_ = idx.Close()
		return
	}

	log.Printf("All files processed: %d chunks total", len(idx.Chunks))

	if err := idx.SaveVectors(vectorsPath); err != nil {
		log.Printf("Failed to save vectors: %v", err)
	}

	s.ingestStatus.mu.Lock()
	s.ingestStatus.Phase = "done"
	s.ingestStatus.ChunksDone = len(idx.Chunks)
	s.ingestStatus.ChunksTotal = len(idx.Chunks)
	s.ingestStatus.mu.Unlock()

	ret := retriever.NewRetriever(idx)

	s.mu.Lock()
	s.activeIndex = idx
	s.activeRetriever = ret
	s.activeProjectID = ProjectID
	// Cache for future instant switches
	s.indexCache.put(ProjectID, &cachedIndex{idx: idx, ret: ret})
	s.mu.Unlock()

	sess, _ := s.projects.Get(ProjectID)
	if sess != nil {
		sess.Status = "ready"
		sess.ChunkCount = len(idx.Chunks)
		_ = s.projects.Update(*sess)
	}

	log.Printf("Ingestion complete for project %s: %d chunks", ProjectID, len(idx.Chunks))
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

	var req struct {
		ProjectID string `json:"project_id"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	s.mu.Lock()
	cancel := s.ingestCancel
	s.mu.Unlock()

	if cancel != nil {
		cancel()
		log.Printf("Ingestion cancel requested by user")
	}

	// Reset session status back to upload
	if req.ProjectID != "" {
		sess, _ := s.projects.Get(req.ProjectID)
		if sess != nil && (sess.Status == "processing" || sess.Status == "upload") {
			sess.Status = "upload"
			_ = s.projects.Update(*sess)
		}
	}

	s.ingestStatus.mu.Lock()
	s.ingestStatus.Phase = "idle"
	s.ingestStatus.Error = ""
	s.ingestStatus.mu.Unlock()

	jsonResp(w, map[string]string{"status": "cancelled"})
}
