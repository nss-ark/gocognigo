package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"

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
	AnswerQuestion(ctx context.Context, question string, results []retriever.Result, summaries []indexer.DocumentSummary, history []ChatMessage, customSystemPrompt ...string) (*Answer, error)
}

// buildSystemPrompt combines the base system prompt with an optional custom
// system prompt. The custom prompt is prepended as additional context/instructions.
func buildSystemPrompt(customSystemPrompt ...string) string {
	if len(customSystemPrompt) > 0 && customSystemPrompt[0] != "" {
		return customSystemPrompt[0] + "\n\n---\n\n" + baseSystemPrompt
	}
	return baseSystemPrompt
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
			model = "Qwen/Qwen2.5-7B-Instruct-1M"
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

Legal & regulatory citation rules:
- When your answer references or relies on a legal provision, regulation, statute, rule, circular, notification, or any regulatory document, you MUST cite the specific section, clause, sub-clause, rule number, or regulation number inline immediately after the relevant sentence or paragraph.
- Format: include the provision in parentheses right after the claim, e.g. "The company must file within 30 days (Section 139(1) of the Companies Act, 2013)[1]" or "This is governed by Regulation 30 of the SEBI (LODR) Regulations, 2015[2]."
- If the source excerpt mentions a specific section or provision number, always reproduce it in your answer — never omit it.
- For multiple provisions in the same paragraph, cite each one inline where it is discussed rather than grouping them at the end.

Conversation context:
- You may receive prior conversation messages (user questions and assistant answers) for context.
- Use them to understand references like "it", "that document", "the same company", "this section", etc.
- Always prioritise the document excerpts provided for factual answers — conversation history is for reference resolution only.

Also keep the legacy fields for backward compatibility:
- "documents": array of all cited document names
- "pages": array of corresponding page numbers`

// isReasoningModel returns true for OpenAI o-series reasoning models
// that do not support temperature, top_p, or response_format parameters.
func isReasoningModel(model string) bool {
	return strings.HasPrefix(model, "o1") ||
		strings.HasPrefix(model, "o3") ||
		strings.HasPrefix(model, "o4")
}

// isAdaptiveThinkingModel returns true for Claude models that use adaptive
// thinking (type: "adaptive"). These models reject the temperature parameter
// and require a thinking block in the API request.
func isAdaptiveThinkingModel(model string) bool {
	return strings.HasPrefix(model, "claude-opus-4-6") ||
		strings.HasPrefix(model, "claude-sonnet-4-6")
}

// isExtendedThinkingModel returns true for older Claude models that support
// extended thinking with type: "enabled" and budget_tokens.
func isExtendedThinkingModel(model string) bool {
	return strings.HasPrefix(model, "claude-opus-4-5") ||
		strings.HasPrefix(model, "claude-opus-4-1") ||
		strings.HasPrefix(model, "claude-sonnet-4-5") ||
		strings.HasPrefix(model, "claude-sonnet-4-2") ||
		strings.HasPrefix(model, "claude-haiku-4-5")
}

// ==========================================
// OpenAI Provider
// ==========================================
type OpenAIProvider struct {
	client *openai.Client
	model  string
}

func (p *OpenAIProvider) AnswerQuestion(ctx context.Context, question string, results []retriever.Result, summaries []indexer.DocumentSummary, history []ChatMessage, customSystemPrompt ...string) (*Answer, error) {
	contextStr := FormatContext(results, summaries)
	userPrompt := fmt.Sprintf("**Question:** %s\n\n**Context:**\n\n%s", question, contextStr)
	sysPrompt := buildSystemPrompt(customSystemPrompt...)

	// Build message list with conversation history
	historyMsgs := buildHistoryMessages(history)

	var resp openai.ChatCompletionResponse
	var err error

	// Retry logic for rate limits (429) and server errors (5xx)
	for attempt := 0; attempt < 5; attempt++ {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		if isReasoningModel(p.model) {
			// Reasoning models (o1, o3, o4 series) reject temperature, top_p,
			// and response_format. Go's zero-value for float32 is 0 which the
			// API interprets as an explicit value, so we must not set it at all.
			// Also use MaxCompletionTokens instead of MaxTokens.
			msgs := []openai.ChatCompletionMessage{
				{Role: openai.ChatMessageRoleSystem, Content: sysPrompt},
			}
			msgs = append(msgs, historyMsgs...)
			msgs = append(msgs, openai.ChatCompletionMessage{Role: openai.ChatMessageRoleUser, Content: userPrompt})
			resp, err = p.client.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
				Model:               p.model,
				Messages:            msgs,
				MaxCompletionTokens: 4096,
			})
		} else {
			msgs := []openai.ChatCompletionMessage{
				{Role: openai.ChatMessageRoleSystem, Content: sysPrompt},
			}
			msgs = append(msgs, historyMsgs...)
			msgs = append(msgs, openai.ChatCompletionMessage{Role: openai.ChatMessageRoleUser, Content: userPrompt})
			resp, err = p.client.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
				Model:          p.model,
				Messages:       msgs,
				Temperature:    0.1,
				ResponseFormat: &openai.ChatCompletionResponseFormat{Type: openai.ChatCompletionResponseFormatTypeJSONObject},
			})
		}

		if err == nil {
			break // Success
		}

		// Check if it's a retriable error (429 Too Many Requests or 5xx Server Error)
		shouldRetry := false
		if apiErr, ok := err.(*openai.APIError); ok {
			if apiErr.HTTPStatusCode == 429 || apiErr.HTTPStatusCode >= 500 {
				shouldRetry = true
			}
		} else if strings.Contains(err.Error(), "429") || strings.Contains(err.Error(), "500") || strings.Contains(err.Error(), "502") || strings.Contains(err.Error(), "503") || strings.Contains(err.Error(), "504") {
			shouldRetry = true
		}

		if shouldRetry {
			wait := time.Duration(2*(1<<uint(attempt))) * time.Second
			if wait > 20*time.Second {
				wait = 20 * time.Second
			}
			log.Printf("OpenAI API error %v (attempt %d/5), retrying in %v...", err, attempt+1, wait)

			timer := time.NewTimer(wait)
			select {
			case <-timer.C:
				continue
			case <-ctx.Done():
				timer.Stop()
				return nil, ctx.Err()
			}
		}

		return nil, fmt.Errorf("openai error: %w", err)
	}

	if err != nil {
		return nil, fmt.Errorf("openai api failed after retries: %w", err)
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

func (p *HuggingFaceProvider) AnswerQuestion(ctx context.Context, question string, results []retriever.Result, summaries []indexer.DocumentSummary, history []ChatMessage, customSystemPrompt ...string) (*Answer, error) {
	contextStr := FormatContext(results, summaries)
	userPrompt := fmt.Sprintf("Question: %s\n\nContext:\n%s", question, contextStr)
	sysPrompt := buildSystemPrompt(customSystemPrompt...)

	// Build messages with history
	messages := []map[string]string{
		{"role": "system", "content": sysPrompt},
	}
	for _, msg := range trimHistory(history) {
		messages = append(messages, map[string]string{"role": msg.Role, "content": msg.Content})
	}
	messages = append(messages, map[string]string{"role": "user", "content": userPrompt})

	reqBody, _ := json.Marshal(map[string]interface{}{
		"model":       p.model,
		"messages":    messages,
		"max_tokens":  2048,
		"temperature": 0.1,
		"stream":      false,
	})

	url := "https://router.huggingface.co/v1/chat/completions"
	var resp *http.Response
	var err error
	client := &http.Client{}

	// Retry logic for rate limits (429) and server errors (5xx)
	for attempt := 0; attempt < 5; attempt++ {
		req, _ := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(reqBody))
		req.Header.Set("Authorization", "Bearer "+p.apiKey)
		req.Header.Set("Content-Type", "application/json")

		resp, err = client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("huggingface req error: %w", err)
		}

		if resp.StatusCode == 200 {
			break
		}

		bodyBytes, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode == 429 || resp.StatusCode >= 500 {
			wait := time.Duration(2*(1<<uint(attempt))) * time.Second
			if wait > 20*time.Second {
				wait = 20 * time.Second
			}
			log.Printf("HuggingFace API error %d (attempt %d/5), retrying in %v...", resp.StatusCode, attempt+1, wait)

			timer := time.NewTimer(wait)
			select {
			case <-timer.C:
				continue
			case <-ctx.Done():
				timer.Stop()
				return nil, ctx.Err()
			}
		}

		return nil, fmt.Errorf("huggingface api error: %d - %s", resp.StatusCode, string(bodyBytes))
	}

	if resp == nil || resp.StatusCode != 200 {
		return nil, fmt.Errorf("huggingface api failed after retries")
	}
	defer resp.Body.Close()

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

func (p *AnthropicProvider) AnswerQuestion(ctx context.Context, question string, results []retriever.Result, summaries []indexer.DocumentSummary, history []ChatMessage, customSystemPrompt ...string) (*Answer, error) {
	contextStr := FormatContext(results, summaries)
	userPrompt := fmt.Sprintf("Question: %s\n\nContext:\n%s", question, contextStr)
	sysPrompt := buildSystemPrompt(customSystemPrompt...)

	// Build messages with history
	anthMessages := []map[string]string{}
	for _, msg := range trimHistory(history) {
		anthMessages = append(anthMessages, map[string]string{"role": msg.Role, "content": msg.Content})
	}
	anthMessages = append(anthMessages, map[string]string{"role": "user", "content": userPrompt})

	// Build request body conditionally based on model capabilities
	reqMap := map[string]interface{}{
		"model":      p.model,
		"max_tokens": 4096,
		"system":     sysPrompt,
		"messages":   anthMessages,
	}

	if isAdaptiveThinkingModel(p.model) {
		// Opus 4.6 / Sonnet 4.6: use adaptive thinking, no temperature allowed
		reqMap["thinking"] = map[string]interface{}{"type": "adaptive"}
		reqMap["max_tokens"] = 16000 // thinking budget comes from max_tokens
		log.Printf("Anthropic: using adaptive thinking for model %s", p.model)
	} else if isExtendedThinkingModel(p.model) {
		// Older thinking models: use enabled + budget_tokens, no temperature
		reqMap["thinking"] = map[string]interface{}{"type": "enabled", "budget_tokens": 10000}
		reqMap["max_tokens"] = 16000
		log.Printf("Anthropic: using extended thinking for model %s", p.model)
	} else {
		// Non-thinking models (e.g. Claude 3 Opus, Claude 3.5 Sonnet)
		reqMap["temperature"] = 0.1
	}

	reqBody, _ := json.Marshal(reqMap)

	var resp *http.Response
	var err error
	client := &http.Client{}

	// Retry logic for rate limits (429) and overloaded (529) errors
	for attempt := 0; attempt < 5; attempt++ {
		req, _ := http.NewRequestWithContext(ctx, "POST", "https://api.anthropic.com/v1/messages", bytes.NewBuffer(reqBody))
		req.Header.Set("x-api-key", p.apiKey)
		req.Header.Set("anthropic-version", "2023-06-01")
		req.Header.Set("content-type", "application/json")

		resp, err = client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("anthropic req error: %w", err)
		}

		if resp.StatusCode == 200 {
			break
		}

		bodyBytes, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode == 429 || resp.StatusCode == 529 {
			wait := time.Duration(2*(1<<uint(attempt))) * time.Second
			if wait > 20*time.Second {
				wait = 20 * time.Second
			}
			log.Printf("Anthropic API error %d (attempt %d/5), retrying in %v...", resp.StatusCode, attempt+1, wait)

			timer := time.NewTimer(wait)
			select {
			case <-timer.C:
				continue
			case <-ctx.Done():
				timer.Stop()
				return nil, ctx.Err()
			}
		}

		return nil, fmt.Errorf("anthropic api error: %d - %s", resp.StatusCode, string(bodyBytes))
	}

	if resp == nil || resp.StatusCode != 200 {
		return nil, fmt.Errorf("anthropic api failed after retries")
	}
	defer resp.Body.Close()

	// Read raw body first for diagnostic logging
	rawBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("anthropic: failed to read response body: %w", err)
	}

	var anthResp struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		StopReason string `json:"stop_reason"`
		Usage      struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
		Error *struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rawBody, &anthResp); err != nil {
		log.Printf("Anthropic raw body (first 500 chars): %.500s", string(rawBody))
		return nil, fmt.Errorf("anthropic json decode error: %w", err)
	}

	// Check for API-level error in response body
	if anthResp.Error != nil {
		return nil, fmt.Errorf("anthropic api error: %s - %s", anthResp.Error.Type, anthResp.Error.Message)
	}

	if len(anthResp.Content) == 0 {
		log.Printf("Anthropic empty content. stop_reason=%s, usage: in=%d out=%d, raw body (first 500): %.500s",
			anthResp.StopReason, anthResp.Usage.InputTokens, anthResp.Usage.OutputTokens, string(rawBody))
		return nil, fmt.Errorf("anthropic empty response (stop_reason: %s)", anthResp.StopReason)
	}

	// Log if response was truncated — helps debug missing output
	if anthResp.StopReason == "max_tokens" {
		log.Printf("WARNING: Anthropic response truncated (stop_reason=max_tokens, output_tokens=%d) for model %s",
			anthResp.Usage.OutputTokens, p.model)
	}

	// Concatenate all text blocks (some models return multiple content blocks)
	var fullText string
	var thinkingText string
	for _, block := range anthResp.Content {
		switch block.Type {
		case "", "text":
			fullText += block.Text
		case "thinking":
			thinkingText += block.Text
		}
	}

	// Log block types for debugging when output looks problematic
	log.Printf("Anthropic response: %d blocks, stop_reason=%s, output_tokens=%d, text_len=%d, thinking_len=%d",
		len(anthResp.Content), anthResp.StopReason, anthResp.Usage.OutputTokens, len(fullText), len(thinkingText))

	// If no text blocks but we have thinking blocks, use thinking as fallback
	if strings.TrimSpace(fullText) == "" && strings.TrimSpace(thinkingText) != "" {
		log.Printf("Anthropic: text blocks empty, using thinking block content as fallback (model: %s)", p.model)
		fullText = thinkingText
	}

	if strings.TrimSpace(fullText) == "" {
		// Log all block types and dump raw body for debugging
		var types []string
		for _, block := range anthResp.Content {
			types = append(types, fmt.Sprintf("type=%q len=%d text=%.50s", block.Type, len(block.Text), block.Text))
		}
		log.Printf("Anthropic empty text! blocks: [%s], raw body (first 500): %.500s", strings.Join(types, ", "), string(rawBody))
		return nil, fmt.Errorf("anthropic: no text content in response (stop_reason: %s, blocks: %d)", anthResp.StopReason, len(anthResp.Content))
	}

	return parseAnswer(fullText, question)
}

// maxHistoryPairs is the number of recent Q&A exchanges to include.
const maxHistoryPairs = 5

// trimHistory returns the last maxHistoryPairs exchanges from the history.
func trimHistory(history []ChatMessage) []ChatMessage {
	max := maxHistoryPairs * 2 // each exchange = 1 user + 1 assistant
	if len(history) <= max {
		return history
	}
	return history[len(history)-max:]
}

// buildHistoryMessages converts ChatMessage history into OpenAI message format.
func buildHistoryMessages(history []ChatMessage) []openai.ChatCompletionMessage {
	trimmed := trimHistory(history)
	var msgs []openai.ChatCompletionMessage
	for _, h := range trimmed {
		role := openai.ChatMessageRoleUser
		if h.Role == "assistant" {
			role = openai.ChatMessageRoleAssistant
		}
		// Truncate long assistant answers to conserve tokens
		content := h.Content
		if h.Role == "assistant" && len(content) > 500 {
			content = content[:500] + "... [truncated]"
		}
		msgs = append(msgs, openai.ChatCompletionMessage{Role: role, Content: content})
	}
	return msgs
}

// Helper to parse JSON from LLM text
func parseAnswer(rawText string, question string) (*Answer, error) {
	rawText = strings.TrimPrefix(rawText, "```json\n")
	rawText = strings.TrimPrefix(rawText, "```\n")
	rawText = strings.Split(rawText, "```")[0]
	rawText = strings.TrimSpace(rawText)

	// If the text doesn't start with {, try to find JSON in it
	if !strings.HasPrefix(rawText, "{") {
		if idx := strings.Index(rawText, "{"); idx >= 0 {
			rawText = rawText[idx:]
		}
	}

	// Use a loose struct that accepts any types for fields the LLM may vary
	var parsed struct {
		Thinking         string          `json:"thinking"`
		Answer           string          `json:"answer"`
		Documents        []string        `json:"documents"`
		Pages            json.RawMessage `json:"pages"`
		Footnotes        json.RawMessage `json:"footnotes"`
		Confidence       float64         `json:"confidence"`
		ConfidenceReason string          `json:"confidence_reason"`
	}
	if err := json.Unmarshal([]byte(rawText), &parsed); err != nil {
		log.Printf("parseAnswer JSON error: %v (first 200 chars: %.200s)", err, rawText)

		// JSON parse failed — try to extract partial "answer" field with regex
		// This handles truncated JSON from max_tokens cutoff
		answerRe := regexp.MustCompile(`"answer"\s*:\s*"((?:[^"\\]|\\.)*)`)
		if m := answerRe.FindStringSubmatch(rawText); len(m) >= 2 {
			partialAnswer := strings.ReplaceAll(m[1], `\"`, `"`)
			partialAnswer = strings.ReplaceAll(partialAnswer, `\\n`, "\n")
			if len(partialAnswer) > 20 {
				log.Printf("parseAnswer: recovered partial answer from truncated JSON (%d chars)", len(partialAnswer))
				// Also try to extract thinking
				thinkingRe := regexp.MustCompile(`"thinking"\s*:\s*"((?:[^"\\]|\\.)*)`)
				var thinking string
				if tm := thinkingRe.FindStringSubmatch(rawText); len(tm) >= 2 {
					thinking = strings.ReplaceAll(tm[1], `\"`, `"`)
					thinking = strings.ReplaceAll(thinking, `\\n`, "\n")
				}
				return &Answer{
					Question:         question,
					Thinking:         thinking,
					Answer:           partialAnswer + " [response truncated]",
					Confidence:       0.5,
					ConfidenceReason: "Response was truncated — answer may be incomplete",
				}, nil
			}
		}

		// Last resort — return the raw text as the answer
		return &Answer{
			Question:   question,
			Answer:     rawText,
			Confidence: 0.5,
		}, nil
	}

	// Parse pages flexibly (could be []int, []string, or mixed)
	var pages []int
	if len(parsed.Pages) > 0 {
		// Try []int first
		if err := json.Unmarshal(parsed.Pages, &pages); err != nil {
			// Fall back: try []interface{} and coerce
			var rawPages []interface{}
			if err2 := json.Unmarshal(parsed.Pages, &rawPages); err2 == nil {
				for _, p := range rawPages {
					switch v := p.(type) {
					case float64:
						pages = append(pages, int(v))
					default:
						pages = append(pages, 0)
					}
				}
			}
		}
	}

	// Parse footnotes flexibly (page field could be int or string)
	var footnotes []Footnote
	if len(parsed.Footnotes) > 0 {
		// Try strict parse first
		if err := json.Unmarshal(parsed.Footnotes, &footnotes); err != nil {
			// Fall back: parse with flexible page type
			var rawFootnotes []struct {
				ID       int         `json:"id"`
				Document string      `json:"document"`
				Page     interface{} `json:"page"`
			}
			if err2 := json.Unmarshal(parsed.Footnotes, &rawFootnotes); err2 == nil {
				for _, fn := range rawFootnotes {
					pageNum := 0
					if v, ok := fn.Page.(float64); ok {
						pageNum = int(v)
					}
					footnotes = append(footnotes, Footnote{
						ID:       fn.ID,
						Document: fn.Document,
						Page:     pageNum,
					})
				}
			}
		}
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
		Pages:            pages,
		Footnotes:        footnotes,
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
