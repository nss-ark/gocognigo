package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"gocognigo/internal/indexer"

	"github.com/sashabaranov/go-openai"
)

// ChatMessage is a lightweight conversation turn used for history context.
type ChatMessage struct {
	Role    string // "user" or "assistant"
	Content string
}

// EnhanceQuery uses a cheap LLM call to rewrite a vague or context-dependent
// question into a self-contained, search-optimised query. It resolves pronouns
// and references using conversation history and adds relevant domain terms
// from the document corpus to improve retrieval.
//
// Returns the enhanced question, or the original question if enhancement
// fails or is unnecessary.
func EnhanceQuery(ctx context.Context, apiKey string, question string, history []ChatMessage, summaries []indexer.DocumentSummary) (string, error) {
	if apiKey == "" {
		return question, nil
	}

	// Build a concise corpus description from document summaries
	var corpusHints []string
	for _, s := range summaries {
		hint := s.Document
		if s.Title != "" {
			hint = s.Title + " (" + s.Document + ")"
		}
		corpusHints = append(corpusHints, hint)
	}
	corpusList := ""
	if len(corpusHints) > 0 {
		corpusList = "\n\nDocuments in the corpus:\n- " + strings.Join(corpusHints, "\n- ")
	}

	// Build conversation history excerpt (last 5 exchanges max)
	var historyLines []string
	start := 0
	if len(history) > 10 {
		start = len(history) - 10
	}
	for _, msg := range history[start:] {
		prefix := "User"
		if msg.Role == "assistant" {
			prefix = "Assistant"
		}
		// Truncate long assistant answers to save tokens
		content := msg.Content
		if msg.Role == "assistant" && len(content) > 200 {
			content = content[:200] + "..."
		}
		historyLines = append(historyLines, fmt.Sprintf("%s: %s", prefix, content))
	}
	historyText := ""
	if len(historyLines) > 0 {
		historyText = "\n\nConversation so far:\n" + strings.Join(historyLines, "\n")
	}

	prompt := fmt.Sprintf(`You are a query enhancement assistant for a legal/regulatory document search system.

Given the user's current question, the conversation history, and the available documents, rewrite the question as a self-contained search query that will work well for document retrieval.

Rules:
1. Resolve ALL pronouns and references ("it", "that", "the same", "this company", etc.) using conversation history
2. If the question mentions a legal section/provision without the full Act name, add the likely Act/regulation name based on context
3. Keep the rewritten query concise but complete — it should make sense on its own without any prior context
4. If the original question is already clear and self-contained, return it unchanged
5. Do NOT answer the question — only rewrite it
6. Preserve the user's intent exactly%s%s

Current question: %s

Respond with ONLY a JSON object: {"enhanced": "your rewritten question here"}`, corpusList, historyText, question)

	client := openai.NewClient(apiKey)
	resp, err := client.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
		Model: "gpt-4o-mini",
		Messages: []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleUser, Content: prompt},
		},
		Temperature:    0.0,
		MaxTokens:      256,
		ResponseFormat: &openai.ChatCompletionResponseFormat{Type: openai.ChatCompletionResponseFormatTypeJSONObject},
	})
	if err != nil {
		log.Printf("EnhanceQuery: LLM call failed (falling back to original): %v", err)
		return question, nil // graceful fallback
	}

	if len(resp.Choices) == 0 {
		return question, nil
	}

	raw := strings.TrimSpace(resp.Choices[0].Message.Content)

	// Parse the JSON response
	var result struct {
		Enhanced string `json:"enhanced"`
	}
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		log.Printf("EnhanceQuery: JSON parse failed (falling back to original): %v (raw: %.200s)", err, raw)
		return question, nil
	}

	enhanced := strings.TrimSpace(result.Enhanced)
	if enhanced == "" {
		return question, nil
	}

	if enhanced != question {
		log.Printf("EnhanceQuery: \"%s\" → \"%s\"", question, enhanced)
	}

	return enhanced, nil
}
