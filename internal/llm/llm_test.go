package llm

import (
	"testing"
)

// ========== parseAnswer ==========

func TestParseAnswer_ValidJSON(t *testing.T) {
	raw := `{
		"thinking": "I need to find revenue data.",
		"answer": "The revenue was $10M.",
		"documents": ["report.pdf"],
		"pages": [5],
		"footnotes": [{"id": 1, "document": "report.pdf", "page": 5}],
		"confidence": 0.9,
		"confidence_reason": "Strong match"
	}`

	got, err := parseAnswer(raw, "What was the revenue?")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Answer != "The revenue was $10M." {
		t.Errorf("answer = %q, want 'The revenue was $10M.'", got.Answer)
	}
	if got.Thinking != "I need to find revenue data." {
		t.Errorf("thinking = %q, want chain-of-thought text", got.Thinking)
	}
	if got.Confidence != 0.9 {
		t.Errorf("confidence = %f, want 0.9", got.Confidence)
	}
	if len(got.Documents) != 1 || got.Documents[0] != "report.pdf" {
		t.Errorf("documents = %v, want [report.pdf]", got.Documents)
	}
	if len(got.Pages) != 1 || got.Pages[0] != 5 {
		t.Errorf("pages = %v, want [5]", got.Pages)
	}
	if len(got.Footnotes) != 1 {
		t.Fatalf("expected 1 footnote, got %d", len(got.Footnotes))
	}
	if got.Footnotes[0].Document != "report.pdf" || got.Footnotes[0].Page != 5 {
		t.Errorf("footnote = %+v, want report.pdf p.5", got.Footnotes[0])
	}
	if got.Question != "What was the revenue?" {
		t.Errorf("question = %q, want original question", got.Question)
	}
}

func TestParseAnswer_WrappedInCodeFence(t *testing.T) {
	raw := "```json\n" + `{
		"answer": "The answer is here.",
		"documents": [],
		"pages": [],
		"confidence": 0.7
	}` + "\n```"

	got, err := parseAnswer(raw, "q")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Answer != "The answer is here." {
		t.Errorf("answer = %q, want 'The answer is here.'", got.Answer)
	}
}

func TestParseAnswer_PrefixedWithText(t *testing.T) {
	raw := `Here is my response:

{
	"answer": "Extracted answer.",
	"documents": ["doc.pdf"],
	"pages": [1],
	"confidence": 0.8
}`

	got, err := parseAnswer(raw, "q")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Answer != "Extracted answer." {
		t.Errorf("answer = %q, expected 'Extracted answer.'", got.Answer)
	}
}

func TestParseAnswer_InvalidJSON_FallsBackToRawText(t *testing.T) {
	raw := "This is not JSON at all, just plain text from the LLM."

	got, err := parseAnswer(raw, "q")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should return the raw text as the answer
	if got.Answer != raw {
		t.Errorf("expected raw text as fallback answer, got %q", got.Answer)
	}
	if got.Confidence != 0.5 {
		t.Errorf("expected 0.5 confidence for fallback, got %f", got.Confidence)
	}
}

func TestParseAnswer_EmptyAnswerField_FallsBackToRawText(t *testing.T) {
	raw := `{
		"answer": "",
		"documents": [],
		"pages": [],
		"confidence": 0.6
	}`

	got, err := parseAnswer(raw, "q")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Empty answer field should fall back to raw JSON text
	if got.Answer == "" {
		t.Error("expected non-empty answer when answer field is empty")
	}
}

func TestParseAnswer_PagesAsStrings(t *testing.T) {
	// Some LLMs return pages as strings instead of ints.
	// parseAnswer uses flexible parsing: tries []int first, then []interface{}.
	raw := `{
		"answer": "Test answer.",
		"documents": ["doc.pdf"],
		"pages": ["5", "10"],
		"confidence": 0.7
	}`

	got, err := parseAnswer(raw, "q")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should parse without crashing; actual page count depends on fallback logic
	if got.Answer != "Test answer." {
		t.Errorf("answer = %q, want 'Test answer.'", got.Answer)
	}
}

func TestParseAnswer_FootnotesWithStringPages(t *testing.T) {
	// Flexible footnote parsing — page as string value.
	// When strict parse fails (page is string not int), falls back to flexible parsing.
	raw := `{
		"answer": "Some answer.",
		"documents": ["doc.pdf"],
		"pages": [3],
		"footnotes": [{"id": 1, "document": "doc.pdf", "page": "3"}],
		"confidence": 0.8
	}`

	got, err := parseAnswer(raw, "q")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Footnotes should be parsed (flexible parser handles string pages)
	if len(got.Footnotes) == 0 {
		t.Fatal("expected at least 1 footnote")
	}
	// First footnote should have correct document
	if got.Footnotes[0].Document != "doc.pdf" {
		t.Errorf("footnote document = %q, want 'doc.pdf'", got.Footnotes[0].Document)
	}
}

func TestParseAnswer_NoFootnotes(t *testing.T) {
	raw := `{
		"answer": "Answer without footnotes.",
		"documents": ["doc.pdf"],
		"pages": [1],
		"confidence": 0.6
	}`

	got, err := parseAnswer(raw, "q")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got.Footnotes) != 0 {
		t.Errorf("expected 0 footnotes, got %d", len(got.Footnotes))
	}
}

func TestParseAnswer_MultipleDocumentsAndPages(t *testing.T) {
	raw := `{
		"answer": "Combined answer from multiple sources.",
		"documents": ["doc1.pdf", "doc2.pdf", "doc3.pdf"],
		"pages": [1, 5, 12],
		"footnotes": [
			{"id": 1, "document": "doc1.pdf", "page": 1},
			{"id": 2, "document": "doc2.pdf", "page": 5},
			{"id": 3, "document": "doc3.pdf", "page": 12}
		],
		"confidence": 0.85,
		"confidence_reason": "Multiple sources corroborate"
	}`

	got, err := parseAnswer(raw, "q")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got.Documents) != 3 {
		t.Errorf("expected 3 documents, got %d", len(got.Documents))
	}
	if len(got.Pages) != 3 {
		t.Errorf("expected 3 pages, got %d", len(got.Pages))
	}
	if len(got.Footnotes) != 3 {
		t.Errorf("expected 3 footnotes, got %d", len(got.Footnotes))
	}
	if got.ConfidenceReason != "Multiple sources corroborate" {
		t.Errorf("confidence_reason = %q, want 'Multiple sources corroborate'", got.ConfidenceReason)
	}
}

// ========== NewProvider ==========

func TestNewProvider_UnknownProvider(t *testing.T) {
	_, err := NewProvider("unknown_provider", "key", "")
	if err == nil {
		t.Error("expected error for unknown provider, got nil")
	}
}

func TestNewProvider_EmptyKey(t *testing.T) {
	// NewProvider creates a provider instance even with empty key.
	// Key validation happens at query time, not at construction.
	p, err := NewProvider("openai", "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p == nil {
		t.Error("expected non-nil provider even with empty key")
	}
}

func TestNewProvider_ValidOpenAI(t *testing.T) {
	p, err := NewProvider("openai", "sk-test-key-123", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p == nil {
		t.Error("expected non-nil provider")
	}
}

func TestNewProvider_ValidAnthropic(t *testing.T) {
	p, err := NewProvider("anthropic", "sk-ant-test-key", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p == nil {
		t.Error("expected non-nil provider")
	}
}
