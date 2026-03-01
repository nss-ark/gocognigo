package main

import (
	"encoding/json"
	"log"
	"net/http"
)

// ========== Project Endpoints ==========

func (s *Server) handleProjects(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		jsonResp(w, s.projects.List())
	case http.MethodPost:
		var req struct {
			Name string `json:"name"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		sess, err := s.projects.Create(req.Name)
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonResp(w, sess)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleActivateProject(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req ProjectIDRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ProjectID == "" {
		jsonErr(w, "chat_id is required", http.StatusBadRequest)
		return
	}

	sess, err := s.projects.Get(req.ProjectID)
	if err != nil {
		jsonErr(w, err.Error(), http.StatusNotFound)
		return
	}

	s.mu.Lock()
	// Don't close previous index — it stays in cache
	s.activeIndex = nil
	s.activeRetriever = nil
	s.activeProjectID = sess.ID
	s.ingestStatus.reset()
	s.indexLoading = false
	s.mu.Unlock()

	// If the project is ready, check cache first then load in background
	if sess.Status == "ready" {
		// Check in-memory cache first — instant if already loaded
		s.mu.Lock()
		if cached, ok := s.indexCache.get(sess.ID); ok {
			s.activeIndex = cached.idx
			s.activeRetriever = cached.ret
			s.mu.Unlock()
			log.Printf("Index cache hit for project %s — instant switch", sess.ID)
		} else {
			// Signal that index is loading
			s.indexLoading = true
			s.mu.Unlock()
			// Load in background
			go func(projectID string) {
				if err := s.loadChatIndexes(projectID); err != nil {
					log.Printf("Warning: could not load indexes for project %s: %v", projectID, err)
				}
				s.mu.Lock()
				s.indexLoading = false
				s.mu.Unlock()
			}(sess.ID)
		}
	}

	// Return project with its conversations
	convs := s.projects.ListConversations(sess.ID)
	jsonResp(w, map[string]interface{}{
		"project":       sess,
		"conversations": convs,
	})
}

func (s *Server) handleDeleteProject(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete && r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req ProjectIDRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ProjectID == "" {
		jsonErr(w, "chat_id is required", http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	// If deleting the project whose index is loaded, cancel ingestion and clear
	if s.activeProjectID == req.ProjectID {
		if s.ingestCancel != nil {
			s.ingestCancel()
			s.ingestCancel = nil
		}
		if s.activeIndex != nil {
			_ = s.activeIndex.Close()
			s.activeIndex = nil
			s.activeRetriever = nil
		}
		s.activeProjectID = ""
		s.ingestStatus.reset()
	}
	// Remove from cache
	s.indexCache.delete(req.ProjectID)
	s.mu.Unlock()

	if err := s.projects.Delete(req.ProjectID); err != nil {
		jsonErr(w, err.Error(), http.StatusNotFound)
		return
	}

	jsonResp(w, map[string]string{"status": "deleted"})
}

func (s *Server) handleRenameProject(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		ProjectID string `json:"chat_id"`
		Name      string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ProjectID == "" || req.Name == "" {
		jsonErr(w, "chat_id and name are required", http.StatusBadRequest)
		return
	}

	sess, err := s.projects.Get(req.ProjectID)
	if err != nil {
		jsonErr(w, err.Error(), http.StatusNotFound)
		return
	}

	sess.Name = req.Name
	if err := s.projects.Update(*sess); err != nil {
		jsonErr(w, err.Error(), http.StatusInternalServerError)
		return
	}

	jsonResp(w, sess)
}
