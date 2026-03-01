package extractor

import (
	"fmt"
	"os"
	"strings"

	"github.com/nguyenthenguyen/docx"
)

// ExtractDOCX extracts text from a DOCX file, splitting into logical pages.
// DOCX files don't have physical page breaks like PDFs, so we group paragraphs
// into ~3000-character blocks to produce meaningful page numbers for citations.
func ExtractDOCX(filePath string) ([]DocumentChunk, error) {
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		return nil, err
	}

	r, err := docx.ReadDocxFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read docx: %w", err)
	}
	defer r.Close()

	doc := r.Editable()
	xmlContent := doc.GetContent()

	// Split XML by paragraph tags to get individual paragraphs
	paragraphs := splitDOCXParagraphs(xmlContent)

	// Group paragraphs into logical pages of ~3000 characters
	const charsPerPage = 3000
	var chunks []DocumentChunk
	var pageBuf strings.Builder
	pageNum := 1

	for _, para := range paragraphs {
		text := strings.TrimSpace(para)
		if text == "" {
			continue
		}

		// If adding this paragraph would exceed the page limit and we already
		// have content, flush the current page first.
		if pageBuf.Len() > 0 && pageBuf.Len()+len(text) > charsPerPage {
			chunks = append(chunks, DocumentChunk{
				PageNumber: pageNum,
				Text:       strings.TrimSpace(pageBuf.String()),
				Document:   fileInfo.Name(),
			})
			pageNum++
			pageBuf.Reset()
		}

		if pageBuf.Len() > 0 {
			pageBuf.WriteString("\n")
		}
		pageBuf.WriteString(text)
	}

	// Flush remaining content
	if pageBuf.Len() > 0 {
		chunks = append(chunks, DocumentChunk{
			PageNumber: pageNum,
			Text:       strings.TrimSpace(pageBuf.String()),
			Document:   fileInfo.Name(),
		})
	}

	// Fallback: if extraction produced nothing, return one empty chunk
	if len(chunks) == 0 {
		chunks = append(chunks, DocumentChunk{
			PageNumber: 1,
			Text:       "",
			Document:   fileInfo.Name(),
		})
	}

	return chunks, nil
}

// splitDOCXParagraphs splits DOCX XML content by <w:p> paragraph tags
// and strips all XML tags from each paragraph, returning clean text.
func splitDOCXParagraphs(xmlStr string) []string {
	// Split on paragraph open tags
	parts := strings.Split(xmlStr, "<w:p")
	var paragraphs []string

	for _, part := range parts {
		cleaned := stripTags(part)
		cleaned = strings.TrimSpace(cleaned)
		if cleaned != "" {
			paragraphs = append(paragraphs, cleaned)
		}
	}

	return paragraphs
}

func stripTags(xmlStr string) string {
	var sb strings.Builder
	inTag := false
	for _, r := range xmlStr {
		if r == '<' {
			inTag = true
			continue
		}
		if r == '>' {
			inTag = false
			continue
		}
		if !inTag {
			sb.WriteRune(r)
		}
	}
	return sb.String()
}
