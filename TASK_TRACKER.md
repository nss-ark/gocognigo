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

### Sprint 5: Hybrid RAG & Chain-of-Thought
- [x] Reciprocal Rank Fusion (k=60) — BM25 + vector search merged without score calibration
- [x] Parent-page deduplication — small chunks for retrieval, full pages for LLM context
- [x] Chain-of-thought reasoning — `thinking` field in LLM responses
- [x] Collapsible reasoning trace UI — expandable panel showing LLM's step-by-step analysis
- [x] Confidence scoring (0.0–1.0) with explanation in every answer

### Sprint 6: Frontend Refactor & Document Management
- [x] Refactored monolithic `app.js` into 9 ES modules (`state.js`, `utils.js`, `ui.js`, `projects.js`, `conversations.js`, `files.js`, `ingest.js`, `query.js`, `app.js`)
- [x] Individual document removal from indexes — removes chunks from vector + BM25 + re-saves vectors
- [x] OpenAI restored as dedicated embedding provider (separate from chat)
- [x] LRU index cache — up to 5 project indexes held in memory for instant switching
- [x] Binary Gob serialization for vectors (5–10x faster loading)
- [x] LLM-generated document summaries — structured metadata (title, type, sections, key entities)
- [x] Concurrent ingestion pipeline — 4 extraction workers, 6 embedding workers, streamed architecture

### Sprint 7: Security & Deployment
- [x] AES-256-GCM encryption for API keys at rest (machine-derived key)
- [x] Path traversal protection on all file operations
- [x] Graceful shutdown with context cancellation propagation
- [x] `start.ps1` auto-installer — Go, Tesseract, Poppler with multiple fallback strategies
- [x] Documentation — comprehensive README with architecture diagrams, API reference, screenshots
- [x] IMPLEMENTATION.md with screenshots placed throughout

### v0.2 Branch: Large Document OCR & API Reliability Fixes
- [x] Chunked OCR processing — `tesseractOCR()` now processes large PDFs in batches of 50 pages
- [x] `pdfPageCount()` helper — uses `pdfinfo` or Go PDF library to determine page count
- [x] `tesseractOCRRange()` — converts page ranges using pdftoppm `-f`/`-l` flags
- [x] Graceful batch failure — failed batches are skipped, remaining pages still processed
- [x] API Retry Logic — added exponential backoff (up to 5 attempts) to Anthropic, OpenAI, and HuggingFace providers in `llm.go` to gracefully handle 429 (Rate Limit) and 5xx (Server Overloaded) errors

### v0.2 Sprint 2: Conversational AI, Streaming & Model Fixes
- [x] Conversation memory — LLM receives last 5 exchanges as context for follow-up questions
- [x] Query enhancement — `EnhanceQuery()` in `enhance.go` uses GPT-4o-mini to rewrite vague/context-dependent questions into self-contained search queries
- [x] Legal citation prompting — system prompt requires inline legal provision citations (Section, Regulation, Rule)
- [x] Claude Opus 4.6 fix — adaptive thinking support (`thinking.type: "adaptive"`), removed incompatible `temperature` parameter, extended thinking for older models
- [x] Streaming responses — SSE-based token-by-token streaming via `/api/query/stream` for all 3 providers (Anthropic, OpenAI, HuggingFace) with real-time UI rendering in `query.js`

---

## v0.2 Roadmap — Open Source Legal AI Solution

### Phase 1: Core Improvements (Current)
- [x] Create `v0.2` branch from `main`
- [x] Fix large document (2000+ pages) processing — chunked OCR
- [x] **Streaming responses** — Stream LLM answers token-by-token instead of waiting for full response
- [x] **Upload progress** — Show per-file upload progress bar (large PDFs can take time)
- [x] **Error recovery on ingestion** — If embedding fails mid-pipeline, allow retry without re-extracting
- [x] **API key validation** — Test API keys on save and show immediate pass/fail feedback
- [x] **Embedding speed optimization** — Connection pooling for HuggingFace, adaptive batch sizing (50 HF / 200 OpenAI), 60s HTTP timeout, provider-specific concurrency (6 HF / 8 OpenAI)

### Phase 2: User Experience
- [x] **Search within documents** — Full-text search across indexed chunks without LLM
- [x] **Export conversations** — Download chat history as PDF or Markdown
- [ ] **Document viewer** — Inline PDF viewer with highlighted citations
- [ ] **Markdown rendering** — Render LLM answers with full Markdown (tables, code blocks)
- [ ] **Dark/light theme toggle** — Currently dark-only
- [ ] **Mobile responsive** — Sidebar collapses on small screens
- [ ] **Keyboard shortcuts** — Ctrl+Enter to send, Ctrl+N for new conversation

### Phase 3: Scalability & Deployment
- [ ] **Docker deployment** — Dockerfile with Tesseract pre-installed
- [ ] **Multi-language OCR** — Configurable Tesseract language packs beyond English
- [ ] **Custom embedding models** — Allow configuring specific model names
- [ ] **Re-index on settings change** — Prompt user to re-process if embedding provider changes
- [ ] **WebSocket for ingestion** — Replace polling with WebSocket push for real-time progress
- [ ] **Test suite** — Unit tests for indexer, retriever, and LLM response parser

### Phase 4: Open Source Release
- [ ] **License change** — Select appropriate open-source license
- [ ] **Documentation overhaul** — Contributing guide, setup for different OSes
- [ ] **Multi-user support** — Auth, separate data directories per user
- [ ] **Production hosting** — Cloud deployment with managed infrastructure
