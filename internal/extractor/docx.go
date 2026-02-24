package extractor

import (
	"fmt"
	"os"
	"strings"

	"github.com/nguyenthenguyen/docx"
)

// ExtractDOCX extracts text from a DOCX file
// DOCX files usually don't have strictly defined semantic "pages" in the same way PDFs do,
// but we will assign PageNumber=1 for the entire document as a fallback, or attempt segmentation.
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
	text := doc.GetContent()

	// Text extraction from XML string in DOCX is rough using GetContent.
	// Since GetContent returns XML, we should strip tags.
	cleanText := stripTags(text)

	// Since DOCX doesn't have inherent pages, we chunk it by chunks of length and assign nominal page blocks.
	// Or we return one chunk and let the indexing chunker split it.
	return []DocumentChunk{
		{
			PageNumber: 1,
			Text:       cleanText,
			Document:   fileInfo.Name(),
		},
	}, nil
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
