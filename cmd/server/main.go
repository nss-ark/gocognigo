package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"gocognigo/internal/chat"
	"gocognigo/internal/extractor"

	"github.com/joho/godotenv"
)

// ========== main ==========

func main() {
	_ = godotenv.Load()

	tesseractOk := extractor.DetectTesseract()
	hasPdftoppm := extractor.DetectPdftoppm()
	
	if tesseractOk && !hasPdftoppm {
		log.Printf("OCR WARNING: Tesseract found but Poppler (pdftoppm) is missing — cannot convert PDFs to images")
	}

	srv := &Server{
		userProjects:  make(map[string]*chat.ProjectStore),
		userSettings:  make(map[string]*SavedSettings),
		ingestStatus:  &IngestStatus{Phase: "idle"},
		tesseractOk:   tesseractOk,
		indexCache:    newLRUCache(maxCacheSize),
	}

	mux := http.NewServeMux()

	// Existing API endpoints
	mux.HandleFunc("/api/query", srv.authMiddleware(srv.handleQuery))
	mux.HandleFunc("/api/query/stream", srv.authMiddleware(srv.handleStreamQuery))
	mux.HandleFunc("/api/batch", srv.authMiddleware(srv.handleBatch))
	mux.HandleFunc("/api/stats", srv.authMiddleware(srv.handleStats))
	mux.HandleFunc("/api/providers", srv.authMiddleware(srv.handleProviders))

	// Upload & ingestion endpoints
	mux.HandleFunc("/api/upload", srv.authMiddleware(srv.handleUpload))
	mux.HandleFunc("/api/ingest", srv.authMiddleware(srv.handleIngest))
	mux.HandleFunc("/api/ingest/status", srv.authMiddleware(srv.handleIngestStatus))
	mux.HandleFunc("/api/ingest/ws", srv.authMiddleware(srv.handleIngestWS))
	mux.HandleFunc("/api/files", srv.authMiddleware(srv.handleFiles))
	mux.HandleFunc("/api/file/view", srv.authMiddleware(srv.handleFileView))
	mux.HandleFunc("/api/ingest/cancel", srv.authMiddleware(srv.handleCancelIngest))
	mux.HandleFunc("/api/ingest/retry", srv.authMiddleware(srv.handleRetryIngest))
	mux.HandleFunc("/api/files/delete", srv.authMiddleware(srv.handleDeleteSingleFile))
	mux.HandleFunc("/api/settings", srv.authMiddleware(srv.handleSettings))
	mux.HandleFunc("/api/settings/validate", srv.authMiddleware(srv.handleValidateKey))
	mux.HandleFunc("/api/search", srv.authMiddleware(srv.handleSearch))
	mux.HandleFunc("/api/conversations/export", srv.authMiddleware(srv.handleExportConversation))
	mux.HandleFunc("/api/index-status", srv.authMiddleware(srv.handleIndexStatus))

	// Project endpoints
	mux.HandleFunc("/api/chats", srv.authMiddleware(srv.handleProjects))
	mux.HandleFunc("/api/chats/activate", srv.authMiddleware(srv.handleActivateProject))
	mux.HandleFunc("/api/chats/delete", srv.authMiddleware(srv.handleDeleteProject))
	mux.HandleFunc("/api/chats/rename", srv.authMiddleware(srv.handleRenameProject))

	// Conversation endpoints
	mux.HandleFunc("/api/conversations", srv.authMiddleware(srv.handleConversations))
	mux.HandleFunc("/api/conversations/delete", srv.authMiddleware(srv.handleDeleteConversation))
	mux.HandleFunc("/api/conversations/messages", srv.authMiddleware(srv.handleMessages))
	mux.HandleFunc("/api/conversations/rename", srv.authMiddleware(srv.handleRenameConversation))

	// Community endpoints
	mux.HandleFunc("/api/projects/meta", srv.authMiddleware(srv.handleUpdateProjectMeta))
	mux.HandleFunc("/api/projects/publish", srv.authMiddleware(srv.handlePublishProject))
	mux.HandleFunc("/api/community", srv.authMiddleware(srv.handleCommunityHub))
	mux.HandleFunc("/api/community/clone", srv.authMiddleware(srv.handleCloneProject))
	mux.HandleFunc("/api/community/tags", srv.authMiddleware(srv.handleCommunityTags))

	// Feedback / feature requests
	mux.HandleFunc("/api/feedback", srv.authMiddleware(srv.handleFeedback))

	// Auth endpoints (public)
	mux.HandleFunc("/api/auth/config", srv.handleAuthConfig)

	// Static files
	mux.Handle("/", http.FileServer(http.Dir("web")))

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	httpSrv := &http.Server{
		Addr:    ":" + port,
		Handler: corsMiddleware(mux),
	}

	// Graceful shutdown: listen for SIGINT/SIGTERM
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	// Start server in a goroutine
	go func() {
		log.Printf("GoCognigo server starting on http://localhost:%s", port)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server error: %v", err)
		}
	}()

	// Block until signal received
	sig := <-stop
	log.Printf("Received %v signal, shutting down...", sig)

	// Cancel any active ingestion
	srv.mu.Lock()
	if srv.ingestCancel != nil {
		log.Printf("Cancelling active ingestion...")
		srv.ingestCancel()
	}
	srv.mu.Unlock()

	// Graceful shutdown with 10-second deadline for in-flight requests
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := httpSrv.Shutdown(ctx); err != nil {
		log.Printf("Shutdown error: %v", err)
	} else {
		log.Printf("Server stopped gracefully")
	}
}
