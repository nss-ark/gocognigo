package chat

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func tempStore(t *testing.T) (*ProjectStore, string) {
	t.Helper()
	dir := t.TempDir()
	store, err := NewProjectStore(filepath.Join(dir, "projects"))
	if err != nil {
		t.Fatalf("failed to create ProjectStore: %v", err)
	}
	return store, dir
}

// ========== Project CRUD ==========

func TestCreateProject(t *testing.T) {
	store, _ := tempStore(t)
	proj, err := store.Create("Test Project")
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if proj.Name != "Test Project" {
		t.Errorf("name = %q, want 'Test Project'", proj.Name)
	}
	if proj.ID == "" {
		t.Error("expected non-empty project ID")
	}
	if proj.Status != "upload" {
		t.Errorf("status = %q, want 'upload'", proj.Status)
	}
	if proj.CreatedAt.IsZero() {
		t.Error("expected non-zero CreatedAt")
	}
}

func TestCreateProject_EmptyName(t *testing.T) {
	store, _ := tempStore(t)
	proj, err := store.Create("")
	if err != nil {
		t.Fatalf("Create with empty name should succeed: %v", err)
	}
	if proj.Name == "" {
		// Should get an auto-generated name
		t.Log("Empty name was assigned:", proj.Name)
	}
}

func TestListProjects(t *testing.T) {
	store, _ := tempStore(t)

	// Initially empty
	if got := store.List(); len(got) != 0 {
		t.Fatalf("expected empty list, got %d", len(got))
	}

	store.Create("Project A")
	store.Create("Project B")

	list := store.List()
	if len(list) != 2 {
		t.Fatalf("expected 2 projects, got %d", len(list))
	}
}

func TestGetProject(t *testing.T) {
	store, _ := tempStore(t)
	proj, _ := store.Create("My Project")

	got, err := store.Get(proj.ID)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if got.Name != "My Project" {
		t.Errorf("name = %q, want 'My Project'", got.Name)
	}
}

func TestGetProject_NotFound(t *testing.T) {
	store, _ := tempStore(t)
	_, err := store.Get("nonexistent-id")
	if err == nil {
		t.Error("expected error for nonexistent project, got nil")
	}
}

func TestUpdateProject(t *testing.T) {
	store, _ := tempStore(t)
	proj, _ := store.Create("Original")

	proj.Name = "Updated"
	proj.Status = "ready"
	proj.ChunkCount = 42
	if err := store.Update(*proj); err != nil {
		t.Fatalf("Update failed: %v", err)
	}

	got, _ := store.Get(proj.ID)
	if got.Name != "Updated" {
		t.Errorf("name = %q, want 'Updated'", got.Name)
	}
	if got.Status != "ready" {
		t.Errorf("status = %q, want 'ready'", got.Status)
	}
	if got.ChunkCount != 42 {
		t.Errorf("chunk_count = %d, want 42", got.ChunkCount)
	}
}

func TestDeleteProject(t *testing.T) {
	store, _ := tempStore(t)
	proj, _ := store.Create("To Delete")

	if err := store.Delete(proj.ID); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	// Confirm deleted
	_, err := store.Get(proj.ID)
	if err == nil {
		t.Error("expected error after delete, got nil")
	}
	if len(store.List()) != 0 {
		t.Errorf("expected 0 projects after delete, got %d", len(store.List()))
	}

	// Project directory should be removed
	projDir := store.ProjectDir(proj.ID)
	if _, err := os.Stat(projDir); !os.IsNotExist(err) {
		t.Error("expected project directory to be removed")
	}
}

func TestDeleteProject_NotFound(t *testing.T) {
	store, _ := tempStore(t)
	err := store.Delete("nonexistent")
	if err == nil {
		t.Error("expected error deleting nonexistent project")
	}
}

// ========== Conversation CRUD ==========

func TestCreateConversation(t *testing.T) {
	store, _ := tempStore(t)
	proj, _ := store.Create("Project")

	conv, err := store.CreateConversation(proj.ID, "Chat 1")
	if err != nil {
		t.Fatalf("CreateConversation failed: %v", err)
	}
	if conv.Name != "Chat 1" {
		t.Errorf("name = %q, want 'Chat 1'", conv.Name)
	}
	if conv.ProjectID != proj.ID {
		t.Errorf("project_id = %q, want %q", conv.ProjectID, proj.ID)
	}
}

func TestListConversations(t *testing.T) {
	store, _ := tempStore(t)
	proj, _ := store.Create("Project")

	store.CreateConversation(proj.ID, "Conv A")
	store.CreateConversation(proj.ID, "Conv B")

	convs := store.ListConversations(proj.ID)
	if len(convs) != 2 {
		t.Fatalf("expected 2 conversations, got %d", len(convs))
	}
}

func TestDeleteConversation(t *testing.T) {
	store, _ := tempStore(t)
	proj, _ := store.Create("Project")
	conv, _ := store.CreateConversation(proj.ID, "To Delete")

	err := store.DeleteConversation(proj.ID, conv.ID)
	if err != nil {
		t.Fatalf("DeleteConversation failed: %v", err)
	}

	convs := store.ListConversations(proj.ID)
	if len(convs) != 0 {
		t.Errorf("expected 0 conversations after delete, got %d", len(convs))
	}
}

// ========== Messages ==========

func TestSaveAndLoadMessages(t *testing.T) {
	store, _ := tempStore(t)
	proj, _ := store.Create("Project")
	conv, _ := store.CreateConversation(proj.ID, "Conv")

	msg1 := Message{Role: "user", Content: "Hello", Timestamp: time.Now()}
	msg2 := Message{Role: "assistant", Content: "Hi there!", Timestamp: time.Now()}

	if err := store.SaveMessage(proj.ID, conv.ID, msg1); err != nil {
		t.Fatalf("SaveMessage failed: %v", err)
	}
	if err := store.SaveMessage(proj.ID, conv.ID, msg2); err != nil {
		t.Fatalf("SaveMessage failed: %v", err)
	}

	msgs, err := store.LoadMessages(proj.ID, conv.ID)
	if err != nil {
		t.Fatalf("LoadMessages failed: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	if msgs[0].Role != "user" || msgs[0].Content != "Hello" {
		t.Errorf("msg[0] = %+v, want user/Hello", msgs[0])
	}
	if msgs[1].Role != "assistant" || msgs[1].Content != "Hi there!" {
		t.Errorf("msg[1] = %+v, want assistant/Hi there!", msgs[1])
	}
}

func TestLoadMessages_NoMessages(t *testing.T) {
	store, _ := tempStore(t)
	proj, _ := store.Create("Project")
	conv, _ := store.CreateConversation(proj.ID, "Empty")

	msgs, err := store.LoadMessages(proj.ID, conv.ID)
	if err != nil {
		t.Fatalf("LoadMessages failed: %v", err)
	}
	if len(msgs) != 0 {
		t.Errorf("expected 0 messages, got %d", len(msgs))
	}
}

// ========== Path Helpers ==========

func TestPathHelpers(t *testing.T) {
	store, _ := tempStore(t)

	dir := store.ProjectDir("test-id")
	if !filepath.IsAbs(dir) {
		t.Error("ProjectDir should return absolute path")
	}

	uploads := store.UploadsDir("test-id")
	if !filepath.IsAbs(uploads) {
		t.Error("UploadsDir should return absolute path")
	}

	bm25 := store.BM25Dir("test-id")
	if !filepath.IsAbs(bm25) {
		t.Error("BM25Dir should return absolute path")
	}

	vectors := store.VectorsPath("test-id")
	if !filepath.IsAbs(vectors) {
		t.Error("VectorsPath should return absolute path")
	}
}

// ========== UUID ==========

func TestGenerateUUID_Unique(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		id := generateUUID()
		if seen[id] {
			t.Fatalf("duplicate UUID generated: %s", id)
		}
		seen[id] = true
	}
}

func TestGenerateUUID_Format(t *testing.T) {
	id := generateUUID()
	if len(id) < 32 {
		t.Errorf("UUID too short: %q (len %d)", id, len(id))
	}
}

// ========== Concurrent Access ==========

func TestConcurrentProjectCreation(t *testing.T) {
	store, _ := tempStore(t)
	var wg sync.WaitGroup

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			store.Create("Concurrent Project")
		}(i)
	}
	wg.Wait()

	if got := len(store.List()); got != 10 {
		t.Errorf("expected 10 projects after concurrent creation, got %d", got)
	}
}
