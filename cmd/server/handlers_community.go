package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// ========== Community Endpoints ==========

// handleUpdateProjectMeta updates a project's community metadata
// (description, tags, system prompt, author).
func (s *Server) handleUpdateProjectMeta(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		ProjectID    string   `json:"project_id"`
		Description  string   `json:"description"`
		Tags         []string `json:"tags"`
		SystemPrompt string   `json:"system_prompt"`
		Author       string   `json:"author"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ProjectID == "" {
		jsonErr(w, "project_id is required", http.StatusBadRequest)
		return
	}

	proj, err := s.projects.Get(req.ProjectID)
	if err != nil {
		jsonErr(w, err.Error(), http.StatusNotFound)
		return
	}

	proj.Description = req.Description
	proj.Tags = req.Tags
	proj.SystemPrompt = req.SystemPrompt
	proj.Author = req.Author

	if err := s.projects.Update(*proj); err != nil {
		jsonErr(w, err.Error(), http.StatusInternalServerError)
		return
	}

	jsonResp(w, proj)
}

// handlePublishProject toggles a project's published state.
func (s *Server) handlePublishProject(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		ProjectID string `json:"project_id"`
		Publish   bool   `json:"publish"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ProjectID == "" {
		jsonErr(w, "project_id is required", http.StatusBadRequest)
		return
	}

	proj, err := s.projects.Get(req.ProjectID)
	if err != nil {
		jsonErr(w, err.Error(), http.StatusNotFound)
		return
	}

	// Require project to be in "ready" state to publish
	if req.Publish && proj.Status != "ready" {
		jsonErr(w, "Project must be fully processed before publishing", http.StatusBadRequest)
		return
	}

	// Require a description to publish
	if req.Publish && strings.TrimSpace(proj.Description) == "" {
		jsonErr(w, "A description is required to publish a project", http.StatusBadRequest)
		return
	}

	proj.Published = req.Publish
	if req.Publish {
		now := time.Now()
		proj.PublishedAt = &now
	} else {
		proj.PublishedAt = nil
	}

	if err := s.projects.Update(*proj); err != nil {
		jsonErr(w, err.Error(), http.StatusInternalServerError)
		return
	}

	jsonResp(w, proj)
}

// handleCommunityHub returns all published projects for the community hub.
func (s *Server) handleCommunityHub(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	published := s.projects.ListPublished()
	if published == nil {
		published = []struct{} // Ensure empty array in JSON
		jsonResp(w, []interface{}{})
		return
	}

	// Filter by tag if query param provided
	tagFilter := r.URL.Query().Get("tag")
	searchQuery := strings.ToLower(r.URL.Query().Get("q"))

	var filtered []interface{}
	for _, p := range published {
		// Apply tag filter
		if tagFilter != "" {
			hasTag := false
			for _, t := range p.Tags {
				if strings.EqualFold(t, tagFilter) {
					hasTag = true
					break
				}
			}
			if !hasTag {
				continue
			}
		}

		// Apply search filter (searches name, description, tags)
		if searchQuery != "" {
			match := strings.Contains(strings.ToLower(p.Name), searchQuery) ||
				strings.Contains(strings.ToLower(p.Description), searchQuery)
			if !match {
				for _, t := range p.Tags {
					if strings.Contains(strings.ToLower(t), searchQuery) {
						match = true
						break
					}
				}
			}
			if !match {
				continue
			}
		}

		// Build a safe response (exclude internal fields)
		filtered = append(filtered, map[string]interface{}{
			"id":            p.ID,
			"name":          p.Name,
			"description":   p.Description,
			"tags":          p.Tags,
			"author":        p.Author,
			"file_count":    p.FileCount,
			"chunk_count":   p.ChunkCount,
			"system_prompt": p.SystemPrompt,
			"published_at":  p.PublishedAt,
			"created_at":    p.CreatedAt,
		})
	}

	if filtered == nil {
		filtered = []interface{}{}
	}

	jsonResp(w, filtered)
}

// handleCloneProject creates a copy of a published project for the current user.
func (s *Server) handleCloneProject(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		SourceID string `json:"source_id"`
		Name     string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.SourceID == "" {
		jsonErr(w, "source_id is required", http.StatusBadRequest)
		return
	}

	newProj, err := s.projects.CloneProject(req.SourceID, req.Name, "")
	if err != nil {
		jsonErr(w, err.Error(), http.StatusBadRequest)
		return
	}

	jsonResp(w, newProj)
}

// handleCommunityTags returns all unique tags across published projects.
func (s *Server) handleCommunityTags(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	published := s.projects.ListPublished()
	tagCounts := make(map[string]int)
	for _, p := range published {
		for _, t := range p.Tags {
			tagCounts[strings.ToLower(t)]++
		}
	}

	type tagInfo struct {
		Tag   string `json:"tag"`
		Count int    `json:"count"`
	}
	var tags []tagInfo
	for t, c := range tagCounts {
		tags = append(tags, tagInfo{Tag: t, Count: c})
	}

	jsonResp(w, tags)
}
