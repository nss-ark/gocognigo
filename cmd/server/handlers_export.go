package main

import (
	"fmt"
	"net/http"
	"strings"
	"time"
)

// handleExportConversation exports a conversation as a formatted Markdown file.
func (s *Server) handleExportConversation(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	projectID := r.URL.Query().Get("project_id")
	convID := r.URL.Query().Get("conversation_id")
	if projectID == "" || convID == "" {
		http.Error(w, "project_id and conversation_id are required", http.StatusBadRequest)
		return
	}

	// Load conversation metadata
	conv, err := s.getProjectStore(r).GetConversation(projectID, convID)
	if err != nil {
		http.Error(w, "Conversation not found", http.StatusNotFound)
		return
	}

	// Load project name
	projectName := projectID
	if proj, err := s.getProjectStore(r).Get(projectID); err == nil {
		projectName = proj.Name
	}

	// Load messages
	msgs, err := s.getProjectStore(r).LoadMessages(projectID, convID)
	if err != nil {
		http.Error(w, "Failed to load messages", http.StatusInternalServerError)
		return
	}

	// Build Markdown
	var sb strings.Builder

	// Header
	convName := conv.Name
	if convName == "" {
		convName = "Untitled Conversation"
	}
	sb.WriteString(fmt.Sprintf("# %s\n\n", convName))
	sb.WriteString(fmt.Sprintf("**Project:** %s  \n", projectName))
	sb.WriteString(fmt.Sprintf("**Date:** %s  \n", conv.CreatedAt.Format("January 2, 2006")))

	// Count Q&A exchanges
	questionCount := 0
	for _, m := range msgs {
		if m.Role == "user" {
			questionCount++
		}
	}
	sb.WriteString(fmt.Sprintf("**Questions:** %d  \n\n", questionCount))
	sb.WriteString("---\n\n")

	// Messages
	for i, msg := range msgs {
		if msg.Role == "user" {
			// Question heading
			sb.WriteString(fmt.Sprintf("## Q: %s\n\n", strings.TrimSpace(msg.Content)))
			if !msg.Timestamp.IsZero() {
				sb.WriteString(fmt.Sprintf("*Asked at %s*\n\n", msg.Timestamp.Format("3:04 PM")))
			}

		} else if msg.Role == "assistant" {
			// Answer
			sb.WriteString("### Answer\n\n")
			sb.WriteString(strings.TrimSpace(msg.Content))
			sb.WriteString("\n\n")

			// Sources from metadata
			if msg.Metadata != nil {
				writeSourcesSection(&sb, msg.Metadata)
				writeConfidenceSection(&sb, msg.Metadata)
				writeFooterSection(&sb, msg.Metadata, msg.Timestamp)
			}

			// Separator between exchanges (but not after the last one)
			if i < len(msgs)-1 {
				sb.WriteString("\n---\n\n")
			}
		}
	}

	// Set download headers
	filename := sanitizeFilename(convName) + ".md"
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	w.Write([]byte(sb.String()))
}

// writeSourcesSection writes the source documents as a bullet list.
func writeSourcesSection(sb *strings.Builder, meta map[string]interface{}) {
	// Try footnotes first (richer data)
	if footnotes, ok := meta["footnotes"]; ok {
		if fnList, ok := footnotes.([]interface{}); ok && len(fnList) > 0 {
			sb.WriteString("**Sources:**\n")
			for _, fn := range fnList {
				if fnMap, ok := fn.(map[string]interface{}); ok {
					doc, _ := fnMap["document"].(string)
					page, _ := fnMap["page"].(float64)
					if doc != "" {
						if page > 0 {
							sb.WriteString(fmt.Sprintf("- %s — p.%d\n", doc, int(page)))
						} else {
							sb.WriteString(fmt.Sprintf("- %s\n", doc))
						}
					}
				}
			}
			sb.WriteString("\n")
			return
		}
	}

	// Fall back to documents/pages arrays
	docs, docsOk := meta["documents"]
	pages, pagesOk := meta["pages"]
	if docsOk {
		if docList, ok := docs.([]interface{}); ok && len(docList) > 0 {
			sb.WriteString("**Sources:**\n")
			var pageList []interface{}
			if pagesOk {
				pageList, _ = pages.([]interface{})
			}
			seen := make(map[string]bool)
			for i, d := range docList {
				doc, _ := d.(string)
				if doc == "" || seen[doc] {
					continue
				}
				seen[doc] = true
				page := 0
				if pageList != nil && i < len(pageList) {
					if p, ok := pageList[i].(float64); ok {
						page = int(p)
					}
				}
				if page > 0 {
					sb.WriteString(fmt.Sprintf("- %s — p.%d\n", doc, page))
				} else {
					sb.WriteString(fmt.Sprintf("- %s\n", doc))
				}
			}
			sb.WriteString("\n")
		}
	}
}

// writeConfidenceSection writes the confidence score and reason.
func writeConfidenceSection(sb *strings.Builder, meta map[string]interface{}) {
	conf, ok := meta["confidence"].(float64)
	if !ok || conf == 0 {
		return
	}
	reason, _ := meta["confidence_reason"].(string)
	if reason != "" {
		sb.WriteString(fmt.Sprintf("**Confidence:** %d%% — %s\n\n", int(conf*100), reason))
	} else {
		sb.WriteString(fmt.Sprintf("**Confidence:** %d%%\n\n", int(conf*100)))
	}
}

// writeFooterSection writes the timing and provider info.
func writeFooterSection(sb *strings.Builder, meta map[string]interface{}, timestamp time.Time) {
	timeSec, _ := meta["time_seconds"].(float64)
	provider, _ := meta["provider"].(string)
	model, _ := meta["model"].(string)

	var parts []string
	if timeSec > 0 {
		parts = append(parts, fmt.Sprintf("%.2fs", timeSec))
	}
	if provider != "" {
		provLabel := provider
		if model != "" {
			provLabel += " / " + model
		}
		parts = append(parts, provLabel)
	}
	if !timestamp.IsZero() {
		parts = append(parts, timestamp.Format("3:04 PM"))
	}

	if len(parts) > 0 {
		sb.WriteString(fmt.Sprintf("*%s*\n", strings.Join(parts, " • ")))
	}
}

// sanitizeFilename removes characters unsafe for filenames.
func sanitizeFilename(name string) string {
	replacer := strings.NewReplacer(
		"/", "-", "\\", "-", ":", "-", "*", "", "?", "",
		"\"", "", "<", "", ">", "", "|", "", "\n", " ",
	)
	name = replacer.Replace(name)
	name = strings.TrimSpace(name)
	if len(name) > 80 {
		name = name[:80]
	}
	if name == "" {
		name = "conversation"
	}
	return name
}
