package indexer

import (
	"strings"
	"testing"

	"gocognigo/internal/extractor"
)

// ========== ChunkPages ==========

func TestChunkPages_ShortPage(t *testing.T) {
	// A page with fewer words than chunkSize should produce exactly 1 chunk
	idx := &Index{}
	text := "This is a short document with only a few words."
	chunks := idx.ChunkPages([]extractor.DocumentChunk{
		{PageNumber: 1, Document: "test.pdf", Text: text},
	})

	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}
	if chunks[0].Text != text {
		t.Errorf("chunk text mismatch: got %q", chunks[0].Text)
	}
	if chunks[0].Document != "test.pdf" {
		t.Errorf("expected document 'test.pdf', got %q", chunks[0].Document)
	}
	if chunks[0].PageNumber != 1 {
		t.Errorf("expected page 1, got %d", chunks[0].PageNumber)
	}
}

func TestChunkPages_ExactlyChunkSize(t *testing.T) {
	// Generate exactly 150 words
	words := make([]string, 150)
	for i := range words {
		words[i] = "word"
	}
	text := strings.Join(words, " ")

	idx := &Index{}
	chunks := idx.ChunkPages([]extractor.DocumentChunk{
		{PageNumber: 1, Document: "test.pdf", Text: text},
	})

	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk for exactly 150 words, got %d", len(chunks))
	}
}

func TestChunkPages_LargePageProducesMultipleChunks(t *testing.T) {
	// Generate 300 words — should produce 2-3 chunks with overlap
	words := make([]string, 300)
	for i := range words {
		words[i] = "word"
	}
	text := strings.Join(words, " ")

	idx := &Index{}
	chunks := idx.ChunkPages([]extractor.DocumentChunk{
		{PageNumber: 1, Document: "test.pdf", Text: text},
	})

	if len(chunks) < 2 {
		t.Fatalf("expected at least 2 chunks for 300 words, got %d", len(chunks))
	}

	// Each chunk (except possibly last) should have ~150 words
	for i, chunk := range chunks {
		wordCount := len(strings.Fields(chunk.Text))
		if i < len(chunks)-1 && wordCount != 150 {
			t.Errorf("chunk %d: expected 150 words, got %d", i, wordCount)
		}
		if wordCount > 150 {
			t.Errorf("chunk %d: exceeded 150 words (got %d)", i, wordCount)
		}
	}
}

func TestChunkPages_ParentTextPreserved(t *testing.T) {
	// Even when text is split into multiple chunks, ParentText should be the full page
	words := make([]string, 300)
	for i := range words {
		words[i] = "word"
	}
	text := strings.Join(words, " ")

	idx := &Index{}
	chunks := idx.ChunkPages([]extractor.DocumentChunk{
		{PageNumber: 1, Document: "test.pdf", Text: text},
	})

	for i, chunk := range chunks {
		if chunk.ParentText != text {
			t.Errorf("chunk %d: ParentText should be full page text", i)
		}
	}
}

func TestChunkPages_MultiplePages(t *testing.T) {
	idx := &Index{}
	chunks := idx.ChunkPages([]extractor.DocumentChunk{
		{PageNumber: 1, Document: "test.pdf", Text: "Page one content here."},
		{PageNumber: 2, Document: "test.pdf", Text: "Page two content here."},
	})

	if len(chunks) != 2 {
		t.Fatalf("expected 2 chunks (one per page), got %d", len(chunks))
	}
	if chunks[0].PageNumber != 1 {
		t.Errorf("first chunk should be page 1, got %d", chunks[0].PageNumber)
	}
	if chunks[1].PageNumber != 2 {
		t.Errorf("second chunk should be page 2, got %d", chunks[1].PageNumber)
	}
}

func TestChunkPages_EmptyInput(t *testing.T) {
	idx := &Index{}
	chunks := idx.ChunkPages(nil)
	if len(chunks) != 0 {
		t.Errorf("expected 0 chunks for nil input, got %d", len(chunks))
	}
}

func TestChunkPages_EmptyText(t *testing.T) {
	idx := &Index{}
	chunks := idx.ChunkPages([]extractor.DocumentChunk{
		{PageNumber: 1, Document: "test.pdf", Text: ""},
	})
	if len(chunks) != 0 {
		t.Errorf("expected 0 chunks for empty text, got %d", len(chunks))
	}
}

func TestChunkPages_UniqueChunkIDs(t *testing.T) {
	words := make([]string, 500)
	for i := range words {
		words[i] = "word"
	}
	text := strings.Join(words, " ")

	idx := &Index{}
	chunks := idx.ChunkPages([]extractor.DocumentChunk{
		{PageNumber: 1, Document: "a.pdf", Text: text},
		{PageNumber: 2, Document: "a.pdf", Text: text},
	})

	ids := make(map[string]bool)
	for _, c := range chunks {
		if ids[c.ID] {
			t.Errorf("duplicate chunk ID: %s", c.ID)
		}
		ids[c.ID] = true
	}
}

// ========== sectionLookup ==========

func TestSectionLookup_MatchesCorrectSection(t *testing.T) {
	sl := sectionLookup{
		summaries: []DocumentSummary{
			{
				Document: "report.pdf",
				Sections: []Section{
					{Name: "Introduction", PageStart: 1, PageEnd: 5},
					{Name: "Analysis", PageStart: 6, PageEnd: 15},
					{Name: "Conclusion", PageStart: 16, PageEnd: 20},
				},
			},
		},
	}

	tests := []struct {
		doc      string
		page     int
		expected string
	}{
		{"report.pdf", 1, "Introduction"},
		{"report.pdf", 5, "Introduction"},
		{"report.pdf", 6, "Analysis"},
		{"report.pdf", 15, "Analysis"},
		{"report.pdf", 20, "Conclusion"},
		{"report.pdf", 21, ""}, // beyond last section
		{"other.pdf", 1, ""},   // wrong document
	}

	for _, tt := range tests {
		got := sl.lookup(tt.doc, tt.page)
		if got != tt.expected {
			t.Errorf("lookup(%q, %d) = %q, want %q", tt.doc, tt.page, got, tt.expected)
		}
	}
}

func TestSectionLookup_EmptySummaries(t *testing.T) {
	sl := sectionLookup{summaries: nil}
	got := sl.lookup("test.pdf", 1)
	if got != "" {
		t.Errorf("expected empty string for nil summaries, got %q", got)
	}
}

// ========== AddDocSummary ==========

func TestAddDocSummary_ThreadSafe(t *testing.T) {
	idx := &Index{}

	// Add summaries concurrently
	done := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func(n int) {
			idx.AddDocSummary(DocumentSummary{
				Document: "doc.pdf",
				Title:    "Test",
			})
			done <- true
		}(i)
	}
	for i := 0; i < 10; i++ {
		<-done
	}

	if len(idx.DocSummaries) != 10 {
		t.Errorf("expected 10 summaries, got %d", len(idx.DocSummaries))
	}
}
