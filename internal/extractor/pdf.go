package extractor

import (
	"bytes"
	"fmt"
	"log"
	"strings"

	"github.com/ledongthuc/pdf"
)

// DocumentChunk represents a piece of extracted text from a document page.
type DocumentChunk struct {
	PageNumber int
	Text       string
	Document   string
}

// ExtractPDF extracts text from a PDF, chunked by page.
// If some or all pages yield no extractable text (scanned PDF), it falls back
// to OCR using the provided OCRConfig — merging OCR'd pages with text pages.
func ExtractPDF(filePath string, ocrCfg *OCRConfig) ([]DocumentChunk, error) {
	f, r, err := pdf.Open(filePath)
	if err != nil {
		// If the Go library can't open the PDF at all, try OCR directly
		if ocrCfg != nil && canRunOCR(ocrCfg) {
			log.Printf("PDF library failed to open %s, attempting OCR fallback: %v", filePath, err)
			return RunOCR(*ocrCfg, filePath)
		}
		return nil, fmt.Errorf("failed to open pdf: %w", err)
	}
	defer f.Close()

	// Get filename
	parts := strings.Split(strings.ReplaceAll(filePath, "\\", "/"), "/")
	fileName := parts[len(parts)-1]

	var chunks []DocumentChunk
	var emptyPages []int // pages that yielded no text (might be scanned)
	numPages := r.NumPage()

	for pageIndex := 1; pageIndex <= numPages; pageIndex++ {
		p := r.Page(pageIndex)
		if p.V.IsNull() {
			emptyPages = append(emptyPages, pageIndex)
			continue
		}

		var buf bytes.Buffer
		str, err := p.GetPlainText(nil)
		if err != nil {
			str = ""
		}

		buf.WriteString(str)
		text := strings.TrimSpace(buf.String())

		if len(text) > 20 { // meaningful text threshold (skip near-empty pages)
			chunks = append(chunks, DocumentChunk{
				PageNumber: pageIndex,
				Document:   fileName,
				Text:       text,
			})
		} else {
			emptyPages = append(emptyPages, pageIndex)
		}
	}

	// Decide whether to run OCR
	if len(chunks) == 0 && numPages > 0 {
		// Fully scanned PDF — no text extracted at all → OCR the entire file
		if ocrCfg != nil && canRunOCR(ocrCfg) {
			log.Printf("No text extracted from %s (%d pages), attempting full OCR", fileName, numPages)
			return RunOCR(*ocrCfg, filePath)
		}
		return nil, fmt.Errorf("no text extracted from %s (scanned PDF? configure OCR in Settings)", fileName)
	}

	if len(emptyPages) > 0 && ocrCfg != nil && canRunOCR(ocrCfg) {
		// Partially scanned PDF — some pages have text, some don't
		// OCR the entire file and merge in pages we're missing
		log.Printf("%s: %d text pages, %d empty pages — running OCR for missing pages", fileName, len(chunks), len(emptyPages))

		ocrChunks, ocrErr := RunOCR(*ocrCfg, filePath)
		if ocrErr != nil {
			log.Printf("OCR fallback failed for %s: %v (continuing with %d text-extracted pages)", fileName, ocrErr, len(chunks))
		} else {
			// Build a set of pages we already have text for
			havePages := make(map[int]bool)
			for _, c := range chunks {
				havePages[c.PageNumber] = true
			}

			// Merge OCR'd pages that we don't already have
			merged := 0
			for _, ocrChunk := range ocrChunks {
				if !havePages[ocrChunk.PageNumber] {
					chunks = append(chunks, ocrChunk)
					merged++
				}
			}
			if merged > 0 {
				log.Printf("Merged %d OCR'd pages into %s", merged, fileName)
			}
		}
	} else if len(emptyPages) > 0 {
		log.Printf("%s: %d pages had no extractable text (no OCR configured, skipping those pages)", fileName, len(emptyPages))
	}

	return chunks, nil
}

// canRunOCR checks if OCR can be attempted with the given config.
// Returns true if an explicit provider is set, OR if Tesseract is available
// (auto-detect mode), OR if a Sarvam key is configured.
func canRunOCR(cfg *OCRConfig) bool {
	if cfg == nil {
		return false
	}
	if cfg.Provider != "" {
		return true
	}
	// Auto-detect: even without explicit provider, try if tools are available
	if cfg.TesseractOk {
		return true
	}
	if cfg.SarvamKey != "" {
		return true
	}
	return false
}
