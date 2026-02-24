package extractor

import (
	"bytes"
	"fmt"
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
// If no text is extractable (scanned PDF), it falls back to OCR
// using the provided OCRConfig.
func ExtractPDF(filePath string, ocrCfg *OCRConfig) ([]DocumentChunk, error) {
	f, r, err := pdf.Open(filePath)
	if err != nil {
		// If the Go library can't open the PDF at all, try OCR directly
		if ocrCfg != nil && ocrCfg.Provider != "" {
			return RunOCR(*ocrCfg, filePath)
		}
		return nil, fmt.Errorf("failed to open pdf: %w", err)
	}
	defer f.Close()

	// Get filename
	parts := strings.Split(strings.ReplaceAll(filePath, "\\", "/"), "/")
	fileName := parts[len(parts)-1]

	var chunks []DocumentChunk
	numPages := r.NumPage()

	for pageIndex := 1; pageIndex <= numPages; pageIndex++ {
		p := r.Page(pageIndex)
		if p.V.IsNull() {
			continue
		}

		var buf bytes.Buffer
		str, err := p.GetPlainText(nil)
		if err != nil {
			str = ""
		}

		buf.WriteString(str)
		text := buf.String()

		if len(strings.TrimSpace(text)) > 0 {
			chunks = append(chunks, DocumentChunk{
				PageNumber: pageIndex,
				Document:   fileName,
				Text:       text,
			})
		}
	}

	// If 0 text extracted but pages exist, try OCR fallback
	if len(chunks) == 0 && numPages > 0 {
		if ocrCfg != nil && ocrCfg.Provider != "" {
			return RunOCR(*ocrCfg, filePath)
		}
		// No OCR configured — return empty
		return nil, fmt.Errorf("no text extracted from %s (scanned PDF? configure OCR in Settings)", fileName)
	}

	return chunks, nil
}
