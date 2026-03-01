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
		indexCache:    newLRUCache(maxCacheSize),
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
