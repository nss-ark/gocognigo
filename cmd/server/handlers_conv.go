package main

import (
	"encoding/json"
	"net/http"

	"gocognigo/internal/chat"
)

// ========== Conversation Endpoints ==========

func (s *Server) handleConversations(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		projectID := r.URL.Query().Get("project_id")
		if projectID == "" {
			jsonErr(w, "project_id is required", http.StatusBadRequest)
			return
		}

		convs := s.projects.ListConversations(projectID)
		if convs == nil {
			convs = []chat.Conversation{}
		}

		jsonResp(w, map[string]interface{}{
			"conversations": convs,
		})

	case http.MethodPost:
		var req struct {
			ProjectID string `json:"project_id"`
			Name      string `json:"name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ProjectID == "" {
			jsonErr(w, "project_id is required", http.StatusBadRequest)
			return
		}

		conv, err := s.projects.CreateConversation(req.ProjectID, req.Name)
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}

		jsonResp(w, conv)

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleDeleteConversation(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		ProjectID      string `json:"project_id"`
		ConversationID string `json:"conversation_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ProjectID == "" {
		jsonErr(w, "project_id is required", http.StatusBadRequest)
		return
	}

	_ = s.projects.DeleteConversation(req.ProjectID, req.ConversationID)
	jsonResp(w, map[string]string{"status": "ok"})
}

func (s *Server) handleMessages(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		ProjectID      string `json:"project_id"`
		ConversationID string `json:"conversation_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ProjectID == "" {
		jsonErr(w, "project_id is required", http.StatusBadRequest)
		return
	}

	msgs, err := s.projects.LoadMessages(req.ProjectID, req.ConversationID)
	if err != nil {
		msgs = []chat.Message{}
	}

	jsonResp(w, msgs)
}

func (s *Server) handleRenameConversation(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		ProjectID      string `json:"project_id"`
		ConversationID string `json:"conversation_id"`
		Name           string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ProjectID == "" {
		jsonErr(w, "project_id is required", http.StatusBadRequest)
		return
	}

	conv, err := s.projects.GetConversation(req.ProjectID, req.ConversationID)
	if err != nil {
		jsonErr(w, err.Error(), http.StatusNotFound)
		return
	}

	conv.Name = req.Name
	if err := s.projects.UpdateConversation(*conv); err != nil {
		jsonErr(w, err.Error(), http.StatusInternalServerError)
		return
	}

	jsonResp(w, conv)
}
