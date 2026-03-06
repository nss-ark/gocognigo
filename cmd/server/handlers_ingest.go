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

	// Remove document chunks from the active index (if loaded for this project)
	chunksRemoved := 0
	s.mu.Lock()
	if s.activeProjectID == req.ProjectID && s.activeIndex != nil {
		chunksRemoved = s.activeIndex.RemoveDocument(clean)

		if chunksRemoved > 0 {
			// Re-save vectors to disk
			vectorsPath := s.projects.VectorsPath(req.ProjectID)
			if err := s.activeIndex.SaveVectors(vectorsPath); err != nil {
				log.Printf("Warning: failed to re-save vectors after document removal: %v", err)
			}

			// Rebuild retriever with cleaned index
			s.activeRetriever = retriever.NewRetriever(s.activeIndex)

			// Update cache
			s.indexCache.put(req.ProjectID, &cachedIndex{idx: s.activeIndex, ret: s.activeRetriever})
		}
	}
	s.mu.Unlock()

	sess, _ := s.projects.Get(req.ProjectID)
	if sess != nil {
		sess.FileCount = fileCount
		if chunksRemoved > 0 {
			sess.ChunkCount -= chunksRemoved
			if sess.ChunkCount < 0 {
				sess.ChunkCount = 0
			}
		}
		_ = s.projects.Update(*sess)
	}

	log.Printf("Deleted file %q from project %s: %d chunks removed, %d files remaining",
		clean, req.ProjectID, chunksRemoved, fileCount)

	jsonResp(w, map[string]interface{}{
		"status":         "deleted",
		"remaining":      fileCount,
		"chunks_removed": chunksRemoved,
	})
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

	// Try to reuse existing index for incremental ingestion
	var idx *indexer.Index
	var existingDocs map[string]bool

	s.mu.Lock()
	if s.activeProjectID == ProjectID && s.activeIndex != nil {
		idx = s.activeIndex
		// Build set of already-indexed document names
		existingDocs = make(map[string]bool)
		for _, c := range idx.Chunks {
			existingDocs[c.Document] = true
		}
	}
	// Also check cache
	if idx == nil {
		if cached, ok := s.indexCache.get(ProjectID); ok {
			idx = cached.idx
			existingDocs = make(map[string]bool)
			for _, c := range idx.Chunks {
				existingDocs[c.Document] = true
			}
		}
	}
	s.mu.Unlock()

	// Filter to only new (unindexed) files
	var newFiles []string
	if existingDocs != nil {
		for _, f := range files {
			if !existingDocs[f] {
				newFiles = append(newFiles, f)
			}
		}
	} else {
		newFiles = files
	}

	if len(newFiles) == 0 {
		// All files are already indexed — just mark done
		log.Printf("All %d files already indexed, nothing to do", len(files))
		s.ingestStatus.mu.Lock()
		s.ingestStatus.Phase = "done"
		s.ingestStatus.Error = ""
		s.ingestStatus.mu.Unlock()

		sess, _ := s.projects.Get(ProjectID)
		if sess != nil {
			sess.Status = "ready"
			_ = s.projects.Update(*sess)
		}
		return
	}

	log.Printf("Incremental ingestion: %d new files, %d already indexed", len(newFiles), len(files)-len(newFiles))

	// Create fresh index only if we don't have one
	if idx == nil {
		// Close old index if it belongs to this project
		s.mu.Lock()
		if s.activeProjectID == ProjectID && s.activeIndex != nil {
			_ = s.activeIndex.Close()
			s.activeIndex = nil
			s.activeRetriever = nil
		}
		s.mu.Unlock()

		// Remove old BM25 index directory so bleve can create a fresh one
		_ = os.RemoveAll(bm25Dir)

		var err error
		idx, err = indexer.NewIndex(s.embedProvider, s.embedAPIKey, "", bm25Dir)
		if err != nil {
			s.ingestStatus.mu.Lock()
			s.ingestStatus.Phase = "error"
			s.ingestStatus.Error = fmt.Sprintf("Failed to create index: %v", err)
			s.ingestStatus.mu.Unlock()
			return
		}
	}

	// Update ingest status for new files only
	s.ingestStatus.mu.Lock()
	s.ingestStatus.FilesTotal = len(newFiles)
	s.ingestStatus.FilesDone = 0
	s.ingestStatus.mu.Unlock()

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

	resultsCh := make(chan extractResult, len(newFiles))
	extractSem := make(chan struct{}, 4)
	var extractWg sync.WaitGroup

	for _, filename := range newFiles {
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

		// Persist chunks to disk so embedding can be retried if it fails
		chunksDir := s.projects.ChunksDir(ProjectID)
		_ = os.MkdirAll(chunksDir, 0755)
		chunkPath := filepath.Join(chunksDir, fileName+".chunks.json")
		if err := indexer.SaveChunks(chunkPath, fileChunks); err != nil {
			log.Printf("Warning: failed to save chunks for %s: %v", fileName, err)
		}

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
		// Save partial progress so user can retry embedding without re-extracting
		if len(idx.Chunks) > 0 {
			if saveErr := idx.SaveVectors(vectorsPath); saveErr != nil {
				log.Printf("Warning: failed to save partial vectors: %v", saveErr)
			} else {
				log.Printf("Saved %d partially-embedded chunks for retry", len(idx.Chunks))
			}
		}

		s.ingestStatus.mu.Lock()
		s.ingestStatus.Phase = "error"
		s.ingestStatus.Error = fmt.Sprintf("Embedding error: %v", firstErr)
		s.ingestStatus.CanRetry = true
		s.ingestStatus.RetryProjectID = ProjectID
		s.ingestStatus.mu.Unlock()

		// Keep the index alive so retry can reuse it
		s.mu.Lock()
		s.activeIndex = idx
		s.activeProjectID = ProjectID
		s.mu.Unlock()
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

	// Clean up chunk files on success
	chunksDir := s.projects.ChunksDir(ProjectID)
	if err := os.RemoveAll(chunksDir); err != nil {
		log.Printf("Warning: failed to clean up chunk files: %v", err)
	}
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
	s.ingestStatus.CanRetry = false
	s.ingestStatus.RetryProjectID = ""
	s.ingestStatus.mu.Unlock()

	jsonResp(w, map[string]string{"status": "cancelled"})
}

// handleRetryIngest retries embedding from saved chunk files (skips extraction).
func (s *Server) handleRetryIngest(w http.ResponseWriter, r *http.Request) {
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

	// Verify retry is available
	snap := s.ingestStatus.snapshot()
	if !snap.CanRetry || snap.RetryProjectID != req.ProjectID {
		jsonErr(w, "No retryable ingestion for this project", http.StatusBadRequest)
		return
	}

	projectID := req.ProjectID
	chunksDir := s.projects.ChunksDir(projectID)
	vectorsPath := s.projects.VectorsPath(projectID)

	// Load saved chunk files
	entries, err := os.ReadDir(chunksDir)
	if err != nil || len(entries) == 0 {
		jsonErr(w, "No saved chunks found for retry — please re-process", http.StatusBadRequest)
		return
	}

	var allChunks []indexer.Chunk
	var chunkFiles []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".chunks.json") {
			continue
		}
		chunkPath := filepath.Join(chunksDir, e.Name())
		chunks, err := indexer.LoadChunks(chunkPath)
		if err != nil {
			log.Printf("Warning: failed to load chunk file %s: %v", e.Name(), err)
			continue
		}
		allChunks = append(allChunks, chunks...)
		chunkFiles = append(chunkFiles, e.Name())
	}

	if len(allChunks) == 0 {
		jsonErr(w, "No valid chunk files found for retry", http.StatusBadRequest)
		return
	}

	log.Printf("Retry: loaded %d chunks from %d chunk files", len(allChunks), len(chunkFiles))

	// Filter out chunks that are already embedded
	s.mu.RLock()
	idx := s.activeIndex
	s.mu.RUnlock()

	var unembed []indexer.Chunk
	if idx != nil {
		existingIDs := make(map[string]bool)
		idx.Lock()
		for _, c := range idx.Chunks {
			existingIDs[c.ID] = true
		}
		idx.Unlock()
		for _, c := range allChunks {
			if !existingIDs[c.ID] {
				unembed = append(unembed, c)
			}
		}
	} else {
		unembed = allChunks
	}

	if len(unembed) == 0 {
		// All chunks already embedded — mark as done
		s.ingestStatus.mu.Lock()
		s.ingestStatus.Phase = "done"
		s.ingestStatus.CanRetry = false
		s.ingestStatus.RetryProjectID = ""
		s.ingestStatus.mu.Unlock()

		jsonResp(w, map[string]string{"status": "already_complete"})
		return
	}

	log.Printf("Retry: %d chunks need embedding (%d already done)", len(unembed), len(allChunks)-len(unembed))

	// Reset ingest status for retry
	s.ingestStatus.mu.Lock()
	s.ingestStatus.Phase = "processing"
	s.ingestStatus.ChunksTotal = len(unembed)
	s.ingestStatus.ChunksDone = 0
	s.ingestStatus.Error = ""
	s.ingestStatus.CanRetry = false
	s.ingestStatus.RetryProjectID = ""
	s.ingestStatus.mu.Unlock()

	// Update session status
	sess, _ := s.projects.Get(projectID)
	if sess != nil {
		sess.Status = "processing"
		_ = s.projects.Update(*sess)
	}

	// Create cancellable context
	ctx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	s.ingestCancel = cancel
	s.mu.Unlock()

	// Run embedding in background
	go s.runRetryEmbedding(ctx, projectID, vectorsPath, idx, unembed)

	jsonResp(w, map[string]string{"status": "retrying"})
}

// runRetryEmbedding runs only the embedding step for a retry.
func (s *Server) runRetryEmbedding(ctx context.Context, projectID, vectorsPath string, idx *indexer.Index, chunks []indexer.Chunk) {
	defer func() {
		s.mu.Lock()
		s.ingestCancel = nil
		s.mu.Unlock()
	}()

	bm25Dir := s.projects.BM25Dir(projectID)

	// Create index if we don't have one
	if idx == nil {
		_ = os.RemoveAll(bm25Dir)
		var err error
		idx, err = indexer.NewIndex(s.embedProvider, s.embedAPIKey, "", bm25Dir)
		if err != nil {
			s.ingestStatus.mu.Lock()
			s.ingestStatus.Phase = "error"
			s.ingestStatus.Error = fmt.Sprintf("Failed to create index: %v", err)
			s.ingestStatus.mu.Unlock()
			return
		}
	}

	// Embed with progress
	embedProgress := func(total, done int) {
		s.ingestStatus.mu.Lock()
		s.ingestStatus.ChunksDone = done
		s.ingestStatus.mu.Unlock()
	}

	if err := idx.EmbedAndIndex(ctx, chunks, embedProgress, 0); err != nil {
		if ctx.Err() != nil {
			s.ingestStatus.mu.Lock()
			s.ingestStatus.Phase = "cancelled"
			s.ingestStatus.Error = "Retry was cancelled"
			s.ingestStatus.mu.Unlock()
			return
		}

		// Save whatever progress was made
		if len(idx.Chunks) > 0 {
			_ = idx.SaveVectors(vectorsPath)
		}

		s.ingestStatus.mu.Lock()
		s.ingestStatus.Phase = "error"
		s.ingestStatus.Error = fmt.Sprintf("Embedding error: %v", err)
		s.ingestStatus.CanRetry = true
		s.ingestStatus.RetryProjectID = projectID
		s.ingestStatus.mu.Unlock()

		s.mu.Lock()
		s.activeIndex = idx
		s.activeProjectID = projectID
		s.mu.Unlock()
		return
	}

	// Success!
	if err := idx.SaveVectors(vectorsPath); err != nil {
		log.Printf("Failed to save vectors: %v", err)
	}

	s.ingestStatus.mu.Lock()
	s.ingestStatus.Phase = "done"
	s.ingestStatus.ChunksDone = len(idx.Chunks)
	s.ingestStatus.ChunksTotal = len(idx.Chunks)
	s.ingestStatus.CanRetry = false
	s.ingestStatus.RetryProjectID = ""
	s.ingestStatus.mu.Unlock()

	ret := retriever.NewRetriever(idx)

	s.mu.Lock()
	s.activeIndex = idx
	s.activeRetriever = ret
	s.activeProjectID = projectID
	s.indexCache.put(projectID, &cachedIndex{idx: idx, ret: ret})
	s.mu.Unlock()

	sess, _ := s.projects.Get(projectID)
	if sess != nil {
		sess.Status = "ready"
		sess.ChunkCount = len(idx.Chunks)
		_ = s.projects.Update(*sess)
	}

	// Clean up chunk files
	chunksDir := s.projects.ChunksDir(projectID)
	_ = os.RemoveAll(chunksDir)

	log.Printf("Retry embedding complete for project %s: %d chunks", projectID, len(idx.Chunks))
}

// ========== File View Endpoint ==========

func (s *Server) handleFileView(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	projectID := r.URL.Query().Get("project_id")
	name := r.URL.Query().Get("name")
	if projectID == "" || name == "" {
		jsonErr(w, "project_id and name are required", http.StatusBadRequest)
		return
	}

	// Prevent path traversal
	clean := filepath.Base(name)
	if clean != name || clean == "." || clean == ".." {
		jsonErr(w, "invalid filename", http.StatusBadRequest)
		return
	}

	// Only serve PDF files
	ext := strings.ToLower(filepath.Ext(clean))
	if ext != ".pdf" {
		jsonErr(w, "only PDF files can be viewed", http.StatusBadRequest)
		return
	}

	uploadsDir := s.projects.UploadsDir(projectID)
	filePath := filepath.Join(uploadsDir, clean)

	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		jsonErr(w, "file not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", fmt.Sprintf("inline; filename=%q", clean))
	http.ServeFile(w, r, filePath)
}
