# GoCognigo — Task Tracker

## Completed

### Sprint 1: Core RAG Pipeline
- [x] Go project scaffolding (`cmd/server/main.go`, internal packages)
- [x] PDF text extraction (`internal/extractor/pdf.go` — ledongthuc/pdf)
- [x] DOCX text extraction (`internal/extractor/docx.go` — nguyenthenguyen/docx)
- [x] Document chunking with overlap (`internal/indexer/indexer.go`)
- [x] BM25 keyword index via Bleve
- [x] Vector embeddings — OpenAI (`text-embedding-3-small`) and HuggingFace providers
- [x] Hybrid retriever — cosine similarity + BM25 score fusion (`internal/retriever/retriever.go`)
- [x] LLM integration — OpenAI, HuggingFace, Anthropic providers (`internal/llm/llm.go`)
- [x] Structured JSON answers with citations, page numbers, confidence scores
- [x] HTTP API server with CORS middleware
- [x] Basic web UI — query input, answer display

### Sprint 2: Upload-First Architecture & Project Management
- [x] Upload-first workflow — drag-and-drop file upload phase
- [x] Per-project file storage (`data/projects/<id>/uploads/`)
- [x] Asynchronous ingestion pipeline with progress polling
- [x] Processing phase UI — progress bar, file/chunk counters, cancel button
- [x] Project CRUD — create, list, activate, rename, delete
- [x] Conversation management — create, list, switch, rename, delete
- [x] Persistent chat history — messages saved per conversation
- [x] Auto-naming conversations from first question
- [x] Sidebar with project list, conversation sub-list, and actions
- [x] Settings panel — LLM provider, embedding provider, API keys
- [x] Settings persistence to `data/settings.json`
- [x] Batch mode — run predefined test question suite
- [x] Provider/model runtime switching
- [x] Indexed files panel in chat phase
- [x] Reset functionality (clear files + indexes)

### Sprint 3: OCR Integration
- [x] Tesseract CLI OCR adapter (PDF → PNG via pdftoppm/ImageMagick → Tesseract)
- [x] Sarvam Vision cloud OCR adapter (base64-encoded PDF → API)
- [x] OCR fallback chain — primary provider fails → try secondary
- [x] OCR provider selection in settings UI
- [x] Tesseract auto-detection on startup

### Sprint 4: Bug Fixes & Polish
- [x] Fixed 401 embedding errors — root cause: truncated API key in `settings.json`
- [x] Fixed UTF-8 mojibake in `app.js` — 8 corrupted character sequences replaced
- [x] Fixed project deletion — added response checking, error feedback, proper state cleanup
- [x] Added empty state UI — "Welcome to GoCognigo" with create CTA when no projects exist
- [x] Added individual file removal before processing — new `POST /api/files/delete` endpoint + UI buttons
- [x] Fixed stale variable references (`chatSessions` → `projects`, `renderChatList` → `renderSidebar`)
- [x] Created `start.ps1` startup script — checks Go, clears port 8080, builds, runs
- [x] Fixed `.env.example` (was empty)

---

## Backlog / Future Work

### High Priority
- [ ] **Streaming responses** — Stream LLM answers token-by-token instead of waiting for full response
- [ ] **Upload progress** — Show per-file upload progress bar (large PDFs can take time)
- [ ] **Error recovery on ingestion** — If embedding fails mid-pipeline, allow retry without re-extracting
- [ ] **API key validation** — Test API keys on save and show immediate pass/fail feedback
- [ ] **Secure key storage** — Encrypt API keys at rest in `settings.json` instead of plaintext

### Medium Priority
- [ ] **Search within documents** — Full-text search across indexed chunks without LLM
- [ ] **Export conversations** — Download chat history as PDF or Markdown
- [ ] **Multi-language OCR** — Configurable Tesseract language packs beyond English
- [ ] **Chunk preview** — Show which chunks were retrieved for an answer (debug view)
- [ ] **Document viewer** — Inline PDF viewer with highlighted citations
- [ ] **Custom embedding models** — Allow configuring specific model names for embeddings
- [ ] **Re-index on settings change** — Prompt user to re-process if embedding provider changes

### Low Priority / Nice to Haves
- [ ] **Dark/light theme toggle** — Currently dark-only
- [ ] **Mobile responsive** — Sidebar collapses on small screens (partially done)
- [ ] **Keyboard shortcuts** — Ctrl+Enter to send, Ctrl+N for new conversation
- [ ] **Drag-and-drop reorder** — Reorder projects in sidebar
- [ ] **Auto-save drafts** — Save in-progress question input
- [ ] **Markdown rendering** — Render LLM answers with full Markdown (tables, code blocks)
- [ ] **WebSocket for ingestion** — Replace polling with WebSocket push for real-time progress
- [ ] **Multi-user support** — Auth, separate data directories per user
- [ ] **Docker deployment** — Dockerfile with Tesseract pre-installed
- [ ] **Test suite** — Unit tests for indexer, retriever, and LLM response parser
