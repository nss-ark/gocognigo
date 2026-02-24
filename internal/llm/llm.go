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
	Answer           string     `json:"answer"`
	Documents        []string   `json:"documents"`
	Pages            []int      `json:"pages"`
	Footnotes        []Footnote `json:"footnotes,omitempty"`
	Confidence       float64    `json:"confidence"`
	ConfidenceReason string     `json:"confidence_reason,omitempty"`
}

// Provider defines the interface for different LLM backends
type Provider interface {
	AnswerQuestion(ctx context.Context, question string, results []retriever.Result) (*Answer, error)
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

// FormatContext builds the context string for prompts
func FormatContext(results []retriever.Result) string {
	var contextParts []string
	for i, r := range results {
		contextParts = append(contextParts, fmt.Sprintf(
			"[Source %d] Document: %s | Page: %d\n%s",
			i+1, r.Document, r.PageNumber, r.Text,
		))
	}
	return strings.Join(contextParts, "\n\n---\n\n")
}

var baseSystemPrompt = `You are a precise document analysis assistant. You will be given a question and relevant excerpts from a corpus of legal, financial, and regulatory documents.

Your task:
1. Answer the question accurately based ONLY on the provided context
2. Use inline footnote markers like [1], [2] in your answer to cite specific claims
3. Be precise — use exact figures, names, and quotes when possible

Respond in this exact JSON format:
{
  "answer": "The revenue was $50B[1] with growth of 12%[2].",
  "footnotes": [
    {"id": 1, "document": "doc1.pdf", "page": 3},
    {"id": 2, "document": "doc2.pdf", "page": 12}
  ],
  "confidence": 0.95,
  "confidence_reason": "Exact figures found in two source documents"
}

Rules:
- Place [N] markers inline where a specific fact comes from that source
- Each footnote has an id (matching the marker), document name, and page number
- confidence is 0.0 to 1.0 based on how well the context answers the question
- confidence_reason is a brief explanation (1 sentence) of why the score is what it is
- If the answer cannot be found in the context, set confidence = 0.0

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

func (p *OpenAIProvider) AnswerQuestion(ctx context.Context, question string, results []retriever.Result) (*Answer, error) {
	contextStr := FormatContext(results)
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

func (p *HuggingFaceProvider) AnswerQuestion(ctx context.Context, question string, results []retriever.Result) (*Answer, error) {
	contextStr := FormatContext(results)
	userPrompt := fmt.Sprintf("Question: %s\n\nContext:\n%s", question, contextStr)

	reqBody, _ := json.Marshal(map[string]interface{}{
		"model": p.model,
		"messages": []map[string]string{
			{"role": "system", "content": baseSystemPrompt},
			{"role": "user", "content": userPrompt},
		},
		"max_tokens":  1024,
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

func (p *AnthropicProvider) AnswerQuestion(ctx context.Context, question string, results []retriever.Result) (*Answer, error) {
	contextStr := FormatContext(results)
	userPrompt := fmt.Sprintf("Question: %s\n\nContext:\n%s", question, contextStr)

	reqBody, _ := json.Marshal(map[string]interface{}{
		"model":       p.model,
		"max_tokens":  1024,
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
		Answer:           answerText,
		Documents:        parsed.Documents,
		Pages:            parsed.Pages,
		Footnotes:        parsed.Footnotes,
		Confidence:       parsed.Confidence,
		ConfidenceReason: parsed.ConfidenceReason,
	}, nil
}
