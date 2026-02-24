package chat

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// ==================== Project ====================

// Project represents a document workspace with its own uploaded files and indexes.
type Project struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	CreatedAt  time.Time `json:"created_at"`
	FileCount  int       `json:"file_count"`
	ChunkCount int       `json:"chunk_count"`
	Status     string    `json:"status"` // "upload", "processing", "ready"
}

// ==================== Conversation ====================

// Conversation represents a Q&A thread within a project.
type Conversation struct {
	ID        string    `json:"id"`
	ProjectID string    `json:"project_id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

// Message represents a single message in a conversation.
type Message struct {
	Role      string                 `json:"role"` // "user" or "assistant"
	Content   string                 `json:"content"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"` // answer data for assistant messages
	Timestamp time.Time              `json:"timestamp"`
}

// ==================== ProjectStore ====================

// ProjectStore manages persistence of projects, conversations, and messages.
type ProjectStore struct {
	mu       sync.RWMutex
	projects []Project
	dataDir  string // e.g. "data/projects"
	filePath string // e.g. "data/projects/projects.json"
}

// NewProjectStore initialises the store, creating directories and loading any existing projects.
func NewProjectStore(dataDir string) (*ProjectStore, error) {
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create data dir: %w", err)
	}

	store := &ProjectStore{
		dataDir:  dataDir,
		filePath: filepath.Join(dataDir, "projects.json"),
	}

	// Load existing projects
	if data, err := os.ReadFile(store.filePath); err == nil {
		_ = json.Unmarshal(data, &store.projects)
	}

	// Migrate: try loading legacy sessions.json
	if len(store.projects) == 0 {
		legacyPath := filepath.Join(dataDir, "sessions.json")
		if data, err := os.ReadFile(legacyPath); err == nil {
			_ = json.Unmarshal(data, &store.projects)
			if len(store.projects) > 0 {
				_ = store.save()
			}
		}
	}

	return store, nil
}

func (s *ProjectStore) save() error {
	data, err := json.MarshalIndent(s.projects, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.filePath, data, 0644)
}

// ==================== Project CRUD ====================

func (s *ProjectStore) Create(name string) (*Project, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	id := generateUUID()
	if name == "" {
		name = "Project " + id[:8]
	}

	project := Project{
		ID:        id,
		Name:      name,
		CreatedAt: time.Now(),
		Status:    "upload",
	}

	// Create per-project directories
	projDir := filepath.Join(s.dataDir, id)
	dirs := []string{
		filepath.Join(projDir, "uploads"),
		filepath.Join(projDir, "conversations"),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0755); err != nil {
			return nil, fmt.Errorf("failed to create project dir %s: %w", d, err)
		}
	}

	s.projects = append(s.projects, project)
	if err := s.save(); err != nil {
		return nil, err
	}

	return &project, nil
}

func (s *ProjectStore) List() []Project {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]Project, len(s.projects))
	copy(result, s.projects)
	return result
}

func (s *ProjectStore) Get(id string) (*Project, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for i := range s.projects {
		if s.projects[i].ID == id {
			p := s.projects[i]
			return &p, nil
		}
	}
	return nil, fmt.Errorf("project not found: %s", id)
}

func (s *ProjectStore) Update(project Project) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i := range s.projects {
		if s.projects[i].ID == project.ID {
			s.projects[i] = project
			return s.save()
		}
	}
	return fmt.Errorf("project not found: %s", project.ID)
}

func (s *ProjectStore) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	found := false
	var updated []Project
	for _, p := range s.projects {
		if p.ID == id {
			found = true
			continue
		}
		updated = append(updated, p)
	}
	if !found {
		return fmt.Errorf("project not found: %s", id)
	}

	s.projects = updated
	projDir := filepath.Join(s.dataDir, id)
	_ = os.RemoveAll(projDir)

	return s.save()
}

// ==================== Conversation CRUD ====================

func (s *ProjectStore) CreateConversation(projectID, name string) (*Conversation, error) {
	// Verify project exists
	if _, err := s.Get(projectID); err != nil {
		return nil, err
	}

	id := generateUUID()
	if name == "" {
		name = "Chat " + id[:8]
	}

	conv := Conversation{
		ID:        id,
		ProjectID: projectID,
		Name:      name,
		CreatedAt: time.Now(),
	}

	// Save conversation metadata
	convDir := filepath.Join(s.dataDir, projectID, "conversations")
	_ = os.MkdirAll(convDir, 0755)

	metaPath := filepath.Join(convDir, id+".meta.json")
	data, _ := json.MarshalIndent(conv, "", "  ")
	if err := os.WriteFile(metaPath, data, 0644); err != nil {
		return nil, fmt.Errorf("failed to save conversation: %w", err)
	}

	// Create empty messages file
	msgsPath := filepath.Join(convDir, id+".json")
	_ = os.WriteFile(msgsPath, []byte("[]"), 0644)

	return &conv, nil
}

func (s *ProjectStore) ListConversations(projectID string) []Conversation {
	convDir := filepath.Join(s.dataDir, projectID, "conversations")
	entries, err := os.ReadDir(convDir)
	if err != nil {
		return nil
	}

	var convs []Conversation
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		// Skip message files, only read .meta.json
		if filepath.Ext(entry.Name()[:len(entry.Name())-5]) != ".meta" {
			continue
		}

		data, err := os.ReadFile(filepath.Join(convDir, entry.Name()))
		if err != nil {
			continue
		}
		var conv Conversation
		if err := json.Unmarshal(data, &conv); err == nil {
			convs = append(convs, conv)
		}
	}
	return convs
}

func (s *ProjectStore) GetConversation(projectID, convID string) (*Conversation, error) {
	metaPath := filepath.Join(s.dataDir, projectID, "conversations", convID+".meta.json")
	data, err := os.ReadFile(metaPath)
	if err != nil {
		return nil, fmt.Errorf("conversation not found: %s", convID)
	}
	var conv Conversation
	if err := json.Unmarshal(data, &conv); err != nil {
		return nil, err
	}
	return &conv, nil
}

func (s *ProjectStore) UpdateConversation(conv Conversation) error {
	metaPath := filepath.Join(s.dataDir, conv.ProjectID, "conversations", conv.ID+".meta.json")
	data, _ := json.MarshalIndent(conv, "", "  ")
	return os.WriteFile(metaPath, data, 0644)
}

func (s *ProjectStore) DeleteConversation(projectID, convID string) error {
	convDir := filepath.Join(s.dataDir, projectID, "conversations")
	_ = os.Remove(filepath.Join(convDir, convID+".meta.json"))
	_ = os.Remove(filepath.Join(convDir, convID+".json"))
	return nil
}

// ==================== Messages ====================

func (s *ProjectStore) LoadMessages(projectID, convID string) ([]Message, error) {
	msgsPath := filepath.Join(s.dataDir, projectID, "conversations", convID+".json")
	data, err := os.ReadFile(msgsPath)
	if err != nil {
		return nil, err
	}
	var msgs []Message
	if err := json.Unmarshal(data, &msgs); err != nil {
		return nil, err
	}
	return msgs, nil
}

func (s *ProjectStore) SaveMessage(projectID, convID string, msg Message) error {
	msgs, _ := s.LoadMessages(projectID, convID)
	msgs = append(msgs, msg)
	return s.saveMessages(projectID, convID, msgs)
}

func (s *ProjectStore) saveMessages(projectID, convID string, msgs []Message) error {
	msgsPath := filepath.Join(s.dataDir, projectID, "conversations", convID+".json")
	data, err := json.MarshalIndent(msgs, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(msgsPath, data, 0644)
}

// ==================== Path Helpers ====================

func (s *ProjectStore) ProjectDir(id string) string {
	return filepath.Join(s.dataDir, id)
}

func (s *ProjectStore) UploadsDir(id string) string {
	return filepath.Join(s.dataDir, id, "uploads")
}

func (s *ProjectStore) BM25Dir(id string) string {
	return filepath.Join(s.dataDir, id, "bm25.index")
}

func (s *ProjectStore) VectorsPath(id string) string {
	return filepath.Join(s.dataDir, id, "vectors.json")
}

// ==================== UUID ====================

func generateUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
