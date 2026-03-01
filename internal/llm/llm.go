package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"

	"gocognigo/internal/indexer"
	"gocognigo/internal/retriever"

	"github.com/sashabaranov/go-openai"
)

// Footnote represents a single inline citation
type Footnote struct {
	ID       int    `json:"id"`
	Document string `json:"document"`
	Page     int    `json:"page"`
}

// Answer represents a structured LLM response
type Answer struct {
	Question         string     `json:"question"`
	Thinking         string     `json:"thinking,omitempty"`
	Answer           string     `json:"answer"`
	Documents        []string   `json:"documents"`
	Pages            []int      `json:"pages"`
	Footnotes        []Footnote `json:"footnotes,omitempty"`
	Confidence       float64    `json:"confidence"`
	ConfidenceReason string     `json:"confidence_reason,omitempty"`
}

// Provider defines the interface for different LLM backends
type Provider interface {
	AnswerQuestion(ctx context.Context, question string, results []retriever.Result, summaries []indexer.DocumentSummary) (*Answer, error)
}

// NewProvider creates the appropriate LLM provider based on config
func NewProvider(providerName, apiKey, model string) (Provider, error) {
	providerName = strings.ToLower(providerName)
	switch providerName {
	case "openai", "":
		if model == "" {
			model = openai.GPT4o
		}
		return &OpenAIProvider{client: openai.NewClient(apiKey), model: model}, nil
	case "huggingface":
		if model == "" {
			model = "mistralai/Mistral-7B-Instruct-v0.3"
		}
		return &HuggingFaceProvider{apiKey: apiKey, model: model}, nil
	case "anthropic":
		if model == "" {
			model = "claude-opus-4-6"
		}
		return &AnthropicProvider{apiKey: apiKey, model: model}, nil
	default:
		return nil, fmt.Errorf("unknown LLM provider: %s", providerName)
	}
}

// FormatContext builds the context string for prompts.
// Uses ParentText (full page) when available for richer LLM context.
func FormatContext(results []retriever.Result, summaries []indexer.DocumentSummary) string {
	var parts []string

	// Prepend ALL document summaries so the LLM has a complete corpus view
	// (critical for enumeration queries like "how many X are there?")
	if len(summaries) > 0 {
		var docSummaryParts []string
		for _, s := range summaries {
			entry := fmt.Sprintf("📄 %s (%s)\nType: %s\nSummary: %s", s.Document, s.Title, s.DocType, s.Summary)
			if len(s.Sections) > 0 {
				var secNames []string
				for _, sec := range s.Sections {
					secNames = append(secNames, fmt.Sprintf("%s (pp.%d-%d)", sec.Name, sec.PageStart, sec.PageEnd))
				}
				entry += "\nSections: " + strings.Join(secNames, "; ")
			}
			if len(s.KeyEntities) > 0 {
				entry += "\nKey Entities: " + strings.Join(s.KeyEntities, ", ")
			}
			docSummaryParts = append(docSummaryParts, entry)
		}
		if len(docSummaryParts) > 0 {
			parts = append(parts, "=== DOCUMENT OVERVIEWS ===\n\n"+strings.Join(docSummaryParts, "\n\n"))
		}
	}

	// Add retrieved chunks (using parent text for full context)
	parts = append(parts, "\n=== RETRIEVED EXCERPTS ===")
	for i, r := range results {
		text := r.ParentText
		if text == "" {
			text = r.Text // fallback for legacy chunks without parent
		}
		header := fmt.Sprintf("[Source %d] Document: %s | Page: %d", i+1, r.Document, r.PageNumber)
		if r.Section != "" {
			header += " | Section: " + r.Section
		}
		parts = append(parts, fmt.Sprintf("%s\n%s", header, text))
	}
	return strings.Join(parts, "\n\n---\n\n")
}

var baseSystemPrompt = `You are a precise document analysis assistant. You will be given a question and relevant excerpts from a corpus of legal, financial, and regulatory documents.

Your task:
1. THINK step-by-step through the question before answering
2. Answer the question accurately based ONLY on the provided context
3. Use inline footnote markers like [1], [2] in your answer to cite specific claims
4. Be precise — use exact figures, names, and quotes when possible

Respond in this exact JSON format:
{
  "thinking": "Let me analyze the question step by step. First, I need to... [your reasoning process here]",
  "answer": "The revenue was $50B[1] with growth of 12%[2].",
  "footnotes": [
    {"id": 1, "document": "doc1.pdf", "page": 3},
    {"id": 2, "document": "doc2.pdf", "page": 12}
  ],
  "confidence": 0.95,
  "confidence_reason": "Exact figures found in two source documents"
}

Thinking rules:
- In the "thinking" field, reason through the problem step by step
- Identify which sources are relevant and why
- For counting/listing questions: go through EVERY source one by one and track what you find
- For numerical questions: locate the exact figure and verify the label matches what was asked
- Cross-check your findings before writing the final answer
- The thinking field is shown to users who want to verify your reasoning

Answer rules:
- Place [N] markers inline where a specific fact comes from that source
- Each footnote has an id (matching the marker), document name, and page number
- confidence is 0.0 to 1.0 based on how well the context answers the question
- confidence_reason is a brief explanation (1 sentence) of why the score is what it is
- If the answer cannot be found in the context, set confidence = 0.0
- For questions asking to LIST, COUNT, or NAME items: exhaustively scan EVERY source excerpt provided. Do not stop early. Count carefully and verify the total.
- Use exact figures, labels, and terminology from the source documents. Prefer the specific wording in the document (e.g. if a document says "Rs. 4,586,550,000" use that exact figure, not a converted or rounded version)
- When multiple documents are relevant, cross-reference ALL of them before forming your answer

Also keep the legacy fields for backward compatibility:
- "documents": array of all cited document names
- "pages": array of corresponding page numbers`

// ==========================================
// OpenAI Provider
// ==========================================
type OpenAIProvider struct {
	client *openai.Client
	model  string
}

func (p *OpenAIProvider) AnswerQuestion(ctx context.Context, question string, results []retriever.Result, summaries []indexer.DocumentSummary) (*Answer, error) {
	contextStr := FormatContext(results, summaries)
	userPrompt := fmt.Sprintf("**Question:** %s\n\n**Context:**\n\n%s", question, contextStr)

	resp, err := p.client.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
		Model: p.model,
		Messages: []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleSystem, Content: baseSystemPrompt},
			{Role: openai.ChatMessageRoleUser, Content: userPrompt},
		},
		Temperature:    0.1,
		ResponseFormat: &openai.ChatCompletionResponseFormat{Type: openai.ChatCompletionResponseFormatTypeJSONObject},
	})
	if err != nil {
		return nil, fmt.Errorf("openai error: %w", err)
	}

	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("openai empty response")
	}

	return parseAnswer(resp.Choices[0].Message.Content, question)
}

// ==========================================
// HuggingFace Provider (v1/chat/completions)
// ==========================================
type HuggingFaceProvider struct {
	apiKey string
	model  string
}

func (p *HuggingFaceProvider) AnswerQuestion(ctx context.Context, question string, results []retriever.Result, summaries []indexer.DocumentSummary) (*Answer, error) {
	contextStr := FormatContext(results, summaries)
	userPrompt := fmt.Sprintf("Question: %s\n\nContext:\n%s", question, contextStr)

	reqBody, _ := json.Marshal(map[string]interface{}{
		"model": p.model,
		"messages": []map[string]string{
			{"role": "system", "content": baseSystemPrompt},
			{"role": "user", "content": userPrompt},
		},
		"max_tokens":  2048,
		"temperature": 0.1,
		"stream":      false,
	})

	url := "https://router.huggingface.co/hf-inference/v1/chat/completions"
	req, _ := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(reqBody))
	req.Header.Set("Authorization", "Bearer "+p.apiKey)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("huggingface req error: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("huggingface api error: %d - %s", resp.StatusCode, string(bodyBytes))
	}

	var chatResp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		return nil, fmt.Errorf("huggingface json error: %w", err)
	}

	if len(chatResp.Choices) == 0 {
		return nil, fmt.Errorf("huggingface empty response")
	}

	return parseAnswer(chatResp.Choices[0].Message.Content, question)
}

// ==========================================
// Anthropic Provider
// ==========================================
type AnthropicProvider struct {
	apiKey string
	model  string
}

func (p *AnthropicProvider) AnswerQuestion(ctx context.Context, question string, results []retriever.Result, summaries []indexer.DocumentSummary) (*Answer, error) {
	contextStr := FormatContext(results, summaries)
	userPrompt := fmt.Sprintf("Question: %s\n\nContext:\n%s", question, contextStr)

	reqBody, _ := json.Marshal(map[string]interface{}{
		"model":       p.model,
		"max_tokens":  2048,
		"temperature": 0.1,
		"system":      baseSystemPrompt,
		"messages": []map[string]string{
			{"role": "user", "content": userPrompt},
		},
	})

	req, _ := http.NewRequestWithContext(ctx, "POST", "https://api.anthropic.com/v1/messages", bytes.NewBuffer(reqBody))
	req.Header.Set("x-api-key", p.apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("content-type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("anthropic req error: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("anthropic api error: %d - %s", resp.StatusCode, string(bodyBytes))
	}

	var anthResp struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&anthResp); err != nil {
		return nil, fmt.Errorf("anthropic json decode error: %w", err)
	}

	if len(anthResp.Content) == 0 {
		return nil, fmt.Errorf("anthropic empty response")
	}

	// Concatenate all text blocks (some models return multiple content blocks)
	var fullText string
	for _, block := range anthResp.Content {
		if block.Type == "" || block.Type == "text" {
			fullText += block.Text
		}
	}

	if fullText == "" {
		return nil, fmt.Errorf("anthropic: no text content in response")
	}

	log.Printf("Anthropic raw response (first 200 chars): %.200s", fullText)
	return parseAnswer(fullText, question)
}

// Helper to parse JSON from LLM text
func parseAnswer(rawText string, question string) (*Answer, error) {
	rawText = strings.TrimPrefix(rawText, "```json\n")
	rawText = strings.TrimPrefix(rawText, "```\n")
	rawText = strings.Split(rawText, "```")[0]
	rawText = strings.TrimSpace(rawText)

	var parsed struct {
		Thinking         string     `json:"thinking"`
		Answer           string     `json:"answer"`
		Documents        []string   `json:"documents"`
		Pages            []int      `json:"pages"`
		Footnotes        []Footnote `json:"footnotes"`
		Confidence       float64    `json:"confidence"`
		ConfidenceReason string     `json:"confidence_reason"`
	}
	if err := json.Unmarshal([]byte(rawText), &parsed); err != nil {
		// JSON parse failed — return the raw text as the answer
		return &Answer{
			Question:   question,
			Answer:     rawText,
			Confidence: 0.5,
		}, nil
	}

	// If the model returned valid JSON but the "answer" field is empty,
	// fall back to showing the full raw text so the user never sees a blank bubble
	answerText := parsed.Answer
	if strings.TrimSpace(answerText) == "" {
		answerText = rawText
	}

	return &Answer{
		Question:         question,
		Thinking:         parsed.Thinking,
		Answer:           answerText,
		Documents:        parsed.Documents,
		Pages:            parsed.Pages,
		Footnotes:        parsed.Footnotes,
		Confidence:       parsed.Confidence,
		ConfidenceReason: parsed.ConfidenceReason,
	}, nil
}

// GenerateDocSummary uses a cheap LLM call to produce a structured document summary.
// It reads the first maxPages of extracted text and returns a DocumentSummary.
func GenerateDocSummary(ctx context.Context, apiKey string, docName string, pages []string, totalPages int) (*indexer.DocumentSummary, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("OpenAI API key required for summary generation")
	}

	// Sample first 5 pages (or all if fewer)
	maxPages := 5
	if len(pages) < maxPages {
		maxPages = len(pages)
	}
	sampleText := strings.Join(pages[:maxPages], "\n\n--- PAGE BREAK ---\n\n")

	// Truncate to ~4000 words to stay within cheap model limits
	words := strings.Fields(sampleText)
	if len(words) > 4000 {
		sampleText = strings.Join(words[:4000], " ")
	}

	prompt := fmt.Sprintf(`Analyze this document and produce a structured summary as JSON.

Document name: %s
Total pages: %d

First %d pages of text:
---
%s
---

Return ONLY valid JSON in this exact format:
{
  "title": "Full document title or case name",
  "type": "legal_case|financial_report|regulatory_filing|contract|transcript|other",
  "summary": "2-3 sentence summary of the document's content and purpose",
  "sections": [
    {"name": "Section Name", "page_start": 1, "page_end": 10}
  ],
  "key_entities": ["entity1", "entity2"]
}

For sections, estimate page ranges based on the content and total page count (%d pages).
If you cannot determine sections, return an empty array.`, docName, totalPages, maxPages, sampleText, totalPages)

	client := openai.NewClient(apiKey)
	resp, err := client.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
		Model: "gpt-4o-mini",
		Messages: []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleUser, Content: prompt},
		},
		Temperature:    0.1,
		ResponseFormat: &openai.ChatCompletionResponseFormat{Type: openai.ChatCompletionResponseFormatTypeJSONObject},
	})
	if err != nil {
		return nil, fmt.Errorf("summary LLM call failed: %w", err)
	}

	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("empty response from summary LLM")
	}

	rawJSON := resp.Choices[0].Message.Content
	rawJSON = strings.TrimPrefix(rawJSON, "```json\n")
	rawJSON = strings.TrimPrefix(rawJSON, "```\n")
	rawJSON = strings.Split(rawJSON, "```")[0]
	rawJSON = strings.TrimSpace(rawJSON)

	var summary struct {
		Title    string `json:"title"`
		DocType  string `json:"type"`
		Summary  string `json:"summary"`
		Sections []struct {
			Name      string `json:"name"`
			PageStart int    `json:"page_start"`
			PageEnd   int    `json:"page_end"`
		} `json:"sections"`
		KeyEntities []string `json:"key_entities"`
	}
	if err := json.Unmarshal([]byte(rawJSON), &summary); err != nil {
		log.Printf("Failed to parse doc summary JSON for %s: %v (raw: %.200s)", docName, err, rawJSON)
		return nil, fmt.Errorf("parse summary: %w", err)
	}

	// Convert to indexer types
	var sections []indexer.Section
	for _, s := range summary.Sections {
		sections = append(sections, indexer.Section{
			Name:      s.Name,
			PageStart: s.PageStart,
			PageEnd:   s.PageEnd,
		})
	}

	return &indexer.DocumentSummary{
		Document:    docName,
		Title:       summary.Title,
		DocType:     summary.DocType,
		Summary:     summary.Summary,
		Sections:    sections,
		KeyEntities: summary.KeyEntities,
	}, nil
}
