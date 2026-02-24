package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gocognigo/internal/extractor"
	"gocognigo/internal/indexer"

	"github.com/joho/godotenv"
)

func main() {
	_ = godotenv.Load() // Ignore error if .env doesn't exist, we will check os.Getenv below
	embedProvider := os.Getenv("EMBEDDING_PROVIDER")
	embedAPIKey := os.Getenv("EMBEDDING_API_KEY")

	if embedAPIKey == "" {
		embedAPIKey = os.Getenv("OPENAI_API_KEY") // Fallback
	}

	if embedAPIKey == "" {
		log.Fatal("Embedding Key (EMBEDDING_API_KEY or OPENAI_API_KEY) environment variable is required")
	}

	index, err := indexer.NewIndex(embedProvider, embedAPIKey, "", "bm25.index")
	if err != nil {
		log.Fatalf("Failed to initialize index: %v", err)
	}

	corpusDir := "corpus"
	files, err := os.ReadDir(corpusDir)
	if err != nil {
		log.Fatalf("Failed to read corpus directory: %v", err)
	}

	start := time.Now()
	for _, file := range files {
		if file.IsDir() {
			continue
		}
		path := filepath.Join(corpusDir, file.Name())

		fmt.Printf("Processing %s...\n", file.Name())

		var chunks []extractor.DocumentChunk
		var extractErr error

		ext := strings.ToLower(filepath.Ext(file.Name()))
		if ext == ".pdf" {
			chunks, extractErr = extractor.ExtractPDF(path)
		} else if ext == ".docx" {
			chunks, extractErr = extractor.ExtractDOCX(path)
		} else {
			continue // Skip other files
		}

		if extractErr != nil {
			log.Printf("Failed to extract %s: %v", file.Name(), extractErr)
			continue
		}

		fmt.Printf("Extracted %d pages from %s\n", len(chunks), file.Name())

		err = index.AddDocument(context.Background(), chunks)
		if err != nil {
			log.Printf("Failed to index %s: %v", file.Name(), err)
		}
	}

	fmt.Printf("Finished ingestion in %v. Saving vector index...\n", time.Since(start))
	if err := index.SaveVectors("vectors.json"); err != nil {
		log.Fatalf("Failed to save vectors: %v", err)
	}
	fmt.Println("Ingestion complete.")
}
