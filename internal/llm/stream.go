package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"gocognigo/internal/indexer"
	"gocognigo/internal/retriever"

	"github.com/sashabaranov/go-openai"
)

// StreamToken represents a single token event in a streaming response.
type StreamToken struct {
	// Token is the text fragment. Empty for non-text events.
	Token string `json:"token,omitempty"`
	// Type differentiates token types: "text", "thinking", "done", "error".
	Type string `json:"type"`
	// Final is the complete Answer, sent only with type="done".
	Final *Answer `json:"final,omitempty"`
	// Error message, sent only with type="error".
	Error string `json:"error,omitempty"`
}

// StreamProvider extends Provider with streaming capability.
type StreamProvider interface {
	Provider
	// StreamAnswer streams tokens to the provided channel. The channel is
	// closed when streaming is complete. The final message on the channel
	// will have Type="done" with the complete Answer.
	StreamAnswer(ctx context.Context, question string, results []retriever.Result, summaries []indexer.DocumentSummary, history []ChatMessage, tokens chan<- StreamToken, customSystemPrompt ...string)
}

// ==========================================
// Anthropic Streaming
// ==========================================

func (p *AnthropicProvider) StreamAnswer(ctx context.Context, question string, results []retriever.Result, summaries []indexer.DocumentSummary, history []ChatMessage, tokens chan<- StreamToken, customSystemPrompt ...string) {
	defer close(tokens)

	contextStr := FormatContext(results, summaries)
	userPrompt := fmt.Sprintf("Question: %s\n\nContext:\n%s", question, contextStr)
	sysPrompt := buildSystemPrompt(customSystemPrompt...)

	// Build messages with history
	anthMessages := []map[string]string{}
	for _, msg := range trimHistory(history) {
		anthMessages = append(anthMessages, map[string]string{"role": msg.Role, "content": msg.Content})
	}
	anthMessages = append(anthMessages, map[string]string{"role": "user", "content": userPrompt})

	reqMap := map[string]interface{}{
		"model":      p.model,
		"max_tokens": 4096,
		"system":     sysPrompt,
		"messages":   anthMessages,
		"stream":     true,
	}

	if isAdaptiveThinkingModel(p.model) {
		reqMap["thinking"] = map[string]interface{}{"type": "adaptive"}
		reqMap["max_tokens"] = 16000
	} else if isExtendedThinkingModel(p.model) {
		reqMap["thinking"] = map[string]interface{}{"type": "enabled", "budget_tokens": 10000}
		reqMap["max_tokens"] = 16000
	} else {
		reqMap["temperature"] = 0.1
	}

	reqBody, _ := json.Marshal(reqMap)

	var resp *http.Response
	var err error
	client := &http.Client{}

	// Retry logic
	for attempt := 0; attempt < 5; attempt++ {
		req, _ := http.NewRequestWithContext(ctx, "POST", "https://api.anthropic.com/v1/messages", bytes.NewBuffer(reqBody))
		req.Header.Set("x-api-key", p.apiKey)
		req.Header.Set("anthropic-version", "2023-06-01")
		req.Header.Set("content-type", "application/json")

		resp, err = client.Do(req)
		if err != nil {
			tokens <- StreamToken{Type: "error", Error: fmt.Sprintf("anthropic req error: %v", err)}
			return
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
			log.Printf("Anthropic streaming API error %d (attempt %d/5), retrying in %v...", resp.StatusCode, attempt+1, wait)
			timer := time.NewTimer(wait)
			select {
			case <-timer.C:
				continue
			case <-ctx.Done():
				timer.Stop()
				tokens <- StreamToken{Type: "error", Error: "request cancelled"}
				return
			}
		}

		tokens <- StreamToken{Type: "error", Error: fmt.Sprintf("anthropic api error: %d - %s", resp.StatusCode, string(bodyBytes))}
		return
	}

	if resp == nil || resp.StatusCode != 200 {
		tokens <- StreamToken{Type: "error", Error: "anthropic api failed after retries"}
		return
	}
	defer resp.Body.Close()

	// Parse SSE stream
	var fullText strings.Builder
	var thinkingText strings.Builder
	scanner := bufio.NewScanner(resp.Body)
	// Increase buffer size for large events
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()

		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}

		var event struct {
			Type  string `json:"type"`
			Index int    `json:"index"`
			Delta *struct {
				Type     string `json:"type"`
				Text     string `json:"text"`
				Thinking string `json:"thinking"`
			} `json:"delta"`
			ContentBlock *struct {
				Type string `json:"type"`
			} `json:"content_block"`
		}

		if err := json.Unmarshal([]byte(data), &event); err != nil {
			continue
		}

		switch event.Type {
		case "content_block_delta":
			if event.Delta != nil {
				switch event.Delta.Type {
				case "text_delta":
					fullText.WriteString(event.Delta.Text)
					tokens <- StreamToken{Type: "text", Token: event.Delta.Text}
				case "thinking_delta":
					thinkingText.WriteString(event.Delta.Thinking)
					tokens <- StreamToken{Type: "thinking", Token: event.Delta.Thinking}
				}
			}
		case "message_stop":
			// Done
		case "error":
			tokens <- StreamToken{Type: "error", Error: fmt.Sprintf("stream error: %s", data)}
			return
		}
	}

	// Parse the accumulated text into a structured answer
	rawText := fullText.String()
	if strings.TrimSpace(rawText) == "" && strings.TrimSpace(thinkingText.String()) != "" {
		rawText = thinkingText.String()
	}

	if strings.TrimSpace(rawText) == "" {
		tokens <- StreamToken{Type: "error", Error: "anthropic: no text content in streamed response"}
		return
	}

	answer, err := parseAnswer(rawText, question)
	if err != nil {
		tokens <- StreamToken{Type: "error", Error: fmt.Sprintf("parse error: %v", err)}
		return
	}

	// Preserve thinking from the stream if parseAnswer didn't capture it
	if answer.Thinking == "" && thinkingText.Len() > 0 {
		answer.Thinking = thinkingText.String()
	}

	tokens <- StreamToken{Type: "done", Final: answer}
}

// ==========================================
// OpenAI Streaming
// ==========================================

func (p *OpenAIProvider) StreamAnswer(ctx context.Context, question string, results []retriever.Result, summaries []indexer.DocumentSummary, history []ChatMessage, tokens chan<- StreamToken, customSystemPrompt ...string) {
	defer close(tokens)

	contextStr := FormatContext(results, summaries)
	userPrompt := fmt.Sprintf("**Question:** %s\n\n**Context:**\n\n%s", question, contextStr)
	sysPrompt := buildSystemPrompt(customSystemPrompt...)

	historyMsgs := buildHistoryMessages(history)
	msgs := []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleSystem, Content: sysPrompt},
	}
	msgs = append(msgs, historyMsgs...)
	msgs = append(msgs, openai.ChatCompletionMessage{Role: openai.ChatMessageRoleUser, Content: userPrompt})

	var stream *openai.ChatCompletionStream
	var err error

	// Retry logic
	for attempt := 0; attempt < 5; attempt++ {
		if ctx.Err() != nil {
			tokens <- StreamToken{Type: "error", Error: "request cancelled"}
			return
		}

		if isReasoningModel(p.model) {
			stream, err = p.client.CreateChatCompletionStream(ctx, openai.ChatCompletionRequest{
				Model:               p.model,
				Messages:            msgs,
				MaxCompletionTokens: 4096,
				Stream:              true,
			})
		} else {
			stream, err = p.client.CreateChatCompletionStream(ctx, openai.ChatCompletionRequest{
				Model:       p.model,
				Messages:    msgs,
				Temperature: 0.1,
				Stream:      true,
			})
		}

		if err == nil {
			break
		}

		shouldRetry := false
		if apiErr, ok := err.(*openai.APIError); ok {
			if apiErr.HTTPStatusCode == 429 || apiErr.HTTPStatusCode >= 500 {
				shouldRetry = true
			}
		} else if strings.Contains(err.Error(), "429") || strings.Contains(err.Error(), "500") {
			shouldRetry = true
		}

		if shouldRetry {
			wait := time.Duration(2*(1<<uint(attempt))) * time.Second
			if wait > 20*time.Second {
				wait = 20 * time.Second
			}
			log.Printf("OpenAI streaming API error %v (attempt %d/5), retrying in %v...", err, attempt+1, wait)
			timer := time.NewTimer(wait)
			select {
			case <-timer.C:
				continue
			case <-ctx.Done():
				timer.Stop()
				tokens <- StreamToken{Type: "error", Error: "request cancelled"}
				return
			}
		}

		tokens <- StreamToken{Type: "error", Error: fmt.Sprintf("openai error: %v", err)}
		return
	}

	if stream == nil {
		tokens <- StreamToken{Type: "error", Error: "openai: failed to create stream after retries"}
		return
	}
	defer stream.Close()

	var fullText strings.Builder

	for {
		response, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			tokens <- StreamToken{Type: "error", Error: fmt.Sprintf("openai stream error: %v", err)}
			return
		}

		if len(response.Choices) > 0 {
			delta := response.Choices[0].Delta.Content
			if delta != "" {
				fullText.WriteString(delta)
				tokens <- StreamToken{Type: "text", Token: delta}
			}
		}
	}

	rawText := fullText.String()
	if strings.TrimSpace(rawText) == "" {
		tokens <- StreamToken{Type: "error", Error: "openai: empty streamed response"}
		return
	}

	answer, err := parseAnswer(rawText, question)
	if err != nil {
		tokens <- StreamToken{Type: "error", Error: fmt.Sprintf("parse error: %v", err)}
		return
	}

	tokens <- StreamToken{Type: "done", Final: answer}
}

// ==========================================
// HuggingFace Streaming
// ==========================================

func (p *HuggingFaceProvider) StreamAnswer(ctx context.Context, question string, results []retriever.Result, summaries []indexer.DocumentSummary, history []ChatMessage, tokens chan<- StreamToken, customSystemPrompt ...string) {
	defer close(tokens)

	contextStr := FormatContext(results, summaries)
	userPrompt := fmt.Sprintf("Question: %s\n\nContext:\n%s", question, contextStr)
	sysPrompt := buildSystemPrompt(customSystemPrompt...)

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
		"stream":      true,
	})

	url := "https://router.huggingface.co/v1/chat/completions"
	var resp *http.Response
	var err error
	client := &http.Client{}

	for attempt := 0; attempt < 5; attempt++ {
		req, _ := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(reqBody))
		req.Header.Set("Authorization", "Bearer "+p.apiKey)
		req.Header.Set("Content-Type", "application/json")

		resp, err = client.Do(req)
		if err != nil {
			tokens <- StreamToken{Type: "error", Error: fmt.Sprintf("huggingface req error: %v", err)}
			return
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
			log.Printf("HuggingFace streaming API error %d (attempt %d/5), retrying in %v...", resp.StatusCode, attempt+1, wait)
			timer := time.NewTimer(wait)
			select {
			case <-timer.C:
				continue
			case <-ctx.Done():
				timer.Stop()
				tokens <- StreamToken{Type: "error", Error: "request cancelled"}
				return
			}
		}

		tokens <- StreamToken{Type: "error", Error: fmt.Sprintf("huggingface api error: %d - %s", resp.StatusCode, string(bodyBytes))}
		return
	}

	if resp == nil || resp.StatusCode != 200 {
		tokens <- StreamToken{Type: "error", Error: "huggingface api failed after retries"}
		return
	}
	defer resp.Body.Close()

	var fullText strings.Builder
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()

		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}

		var event struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
		}

		if err := json.Unmarshal([]byte(data), &event); err != nil {
			continue
		}

		if len(event.Choices) > 0 {
			delta := event.Choices[0].Delta.Content
			if delta != "" {
				fullText.WriteString(delta)
				tokens <- StreamToken{Type: "text", Token: delta}
			}
		}
	}

	rawText := fullText.String()
	if strings.TrimSpace(rawText) == "" {
		tokens <- StreamToken{Type: "error", Error: "huggingface: empty streamed response"}
		return
	}

	answer, err := parseAnswer(rawText, question)
	if err != nil {
		tokens <- StreamToken{Type: "error", Error: fmt.Sprintf("parse error: %v", err)}
		return
	}

	tokens <- StreamToken{Type: "done", Final: answer}
}
