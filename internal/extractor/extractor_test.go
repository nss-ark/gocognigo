package extractor

import (
	"testing"
)

// ========== canRunOCR ==========

func TestCanRunOCR_NilConfig(t *testing.T) {
	if canRunOCR(nil) {
		t.Error("expected false for nil config")
	}
}

func TestCanRunOCR_EmptyConfig(t *testing.T) {
	cfg := &OCRConfig{}
	if canRunOCR(cfg) {
		t.Error("expected false for empty config (no provider, no tesseract, no sarvam key)")
	}
}

func TestCanRunOCR_ExplicitProvider(t *testing.T) {
	cfg := &OCRConfig{Provider: "tesseract"}
	if !canRunOCR(cfg) {
		t.Error("expected true when explicit provider is set")
	}
}

func TestCanRunOCR_TesseractAvailable(t *testing.T) {
	cfg := &OCRConfig{TesseractOk: true}
	if !canRunOCR(cfg) {
		t.Error("expected true when TesseractOk is true")
	}
}

func TestCanRunOCR_SarvamKeySet(t *testing.T) {
	cfg := &OCRConfig{SarvamKey: "sarvam-test-key"}
	if !canRunOCR(cfg) {
		t.Error("expected true when SarvamKey is set")
	}
}

func TestCanRunOCR_AllFieldsSet(t *testing.T) {
	cfg := &OCRConfig{
		Provider:    "sarvam",
		SarvamKey:   "key",
		TesseractOk: true,
	}
	if !canRunOCR(cfg) {
		t.Error("expected true when all fields are set")
	}
}

// ========== stripTags ==========

func TestStripTags_BasicXML(t *testing.T) {
	input := "<w:t>Hello</w:t> <w:t>World</w:t>"
	got := stripTags(input)
	if got != "Hello World" {
		t.Errorf("stripTags = %q, want 'Hello World'", got)
	}
}

func TestStripTags_NoTags(t *testing.T) {
	input := "Just plain text"
	got := stripTags(input)
	if got != input {
		t.Errorf("stripTags = %q, want %q", got, input)
	}
}

func TestStripTags_EmptyString(t *testing.T) {
	got := stripTags("")
	if got != "" {
		t.Errorf("stripTags of empty = %q, want empty", got)
	}
}

func TestStripTags_NestedTags(t *testing.T) {
	input := "<root><child>Content</child></root>"
	got := stripTags(input)
	if got != "Content" {
		t.Errorf("stripTags = %q, want 'Content'", got)
	}
}

func TestStripTags_SelfClosingTags(t *testing.T) {
	input := "Text<br/>More"
	got := stripTags(input)
	if got != "TextMore" {
		t.Errorf("stripTags = %q, want 'TextMore'", got)
	}
}
