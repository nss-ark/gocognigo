<p align="center">
  <h1 align="center">GoCognigo</h1>
  <p align="center">
    <strong>High-Performance Document Intelligence Engine</strong><br/>
    Built in Go · Hybrid RAG Pipeline · Multi-Provider LLM · Dark-Themed Web UI
  </p>
</p>

<p align="center">
  <a href="#features">Features</a> •
  <a href="#quick-start">Quick Start</a> •
  <a href="#architecture">Architecture</a> •
  <a href="#api-reference">API Reference</a> •
  <a href="#tech-stack">Tech Stack</a>
</p>

---

## Inspiration

GoCognigo was built as a response to the [Lucio Challenge](https://www.lucioai.com/lucio-challenge) — a high-intensity engineering sprint that tasks participants with building a document intelligence system capable of ingesting a corpus of 200 documents, structuring them for efficient reasoning, and accurately answering complex questions against ground-truth answers, all under strict performance constraints. The challenge evaluates speed, accuracy, and architectural innovation in document processing pipelines. GoCognigo is built ground up to deliver on those demands.

---

## Features

### 🔍 Hybrid RAG (Retrieval-Augmented Generation)
- **Dual-index retrieval** — Combines BM25 keyword search (via [Bleve](https://blevesearch.com/)) with vector cosine similarity for high-recall, high-precision document retrieval.
- **Reciprocal Rank Fusion (RRF)** — Merges results from both indexes using RRF (k=60), ensuring neither lexical nor semantic matches are missed.
- **Parent-page deduplication** — When multiple small search chunks from the same page match, only the highest-scored one is returned, but the full parent page text is sent to the LLM for richer context.

### 🧠 Multi-Provider LLM Support
- **Three LLM backends** — OpenAI (GPT-4o, o-series reasoning models), Anthropic (Claude Opus 4.6, Sonnet, Haiku family), and HuggingFace Inference API (Qwen, Llama, Phi-4, QwQ).
- **Runtime switching** — Change LLM provider and model on the fly from the UI without restarting the server. Each query can target a different provider/model.
- **Reasoning model awareness** — Automatically detects OpenAI o-series reasoning models and adjusts API parameters accordingly (no `temperature`, `top_p`, or `response_format`; uses `MaxCompletionTokens`).

### 💭 Chain-of-Thought Reasoning
- **Step-by-step thinking** — The LLM is prompted to reason through each question before answering, producing a `thinking` field that shows its analysis process.
- **Collapsible thinking UI** — The frontend displays the reasoning in a collapsible section, allowing users to verify the model's logic without cluttering the answer view.
- **Exhaustive source scanning** — For counting/listing questions, the system prompt instructs the LLM to scan every retrieved excerpt one by one and track findings before finalizing.

### 📄 Document Processing Pipeline
- **Upload-first workflow** — Drag-and-drop PDF and DOCX files into the upload zone. Review, add, or remove files before committing to processing.
- **Multi-format extraction** — PDF text extraction via `ledongthuc/pdf` with page-level chunking; DOCX extraction via `nguyenthenguyen/docx`.
- **Intelligent chunking** — Documents are split into ~150-word search chunks with overlap, each linked to its full parent page text. Chunks are tagged with document name, page number, and section information.
- **LLM-generated document summaries** — On ingestion, the first 5 pages of each document are sent to GPT-4o-mini to produce structured metadata: title, document type (legal case, financial report, regulatory filing, etc.), summary, sections with page ranges, and key entities.
- **Streamed ingestion pipeline** — Extraction, chunking, embedding, and summary generation run concurrently with a semaphore-based concurrency limiter. Up to 4 files are extracted simultaneously, and embedding batches (200 chunks each) use up to 6 concurrent API calls with retry logic.
- **Cancellable processing** — Ingestion can be cancelled mid-pipeline from the UI. Uses Go context propagation to cleanly abort extraction, embedding, and summary generation goroutines.
- **Real-time progress tracking** — The frontend polls `/api/ingest/status` to display file-level and chunk-level progress with a progress bar and per-file success/failure status.

### 🔠 OCR Support for Scanned PDFs
- **Dual OCR providers** — Tesseract OCR (free, local) and Sarvam Vision Document Intelligence API (paid, cloud).
- **Smart auto-detection** — On startup, the system probes for Tesseract and Poppler (`pdftoppm`) binaries on the PATH and common install directories. If Tesseract is found, it's preferred as the default; otherwise falls back to Sarvam if an API key is configured.
- **Fallback chain** — If the primary OCR provider fails for a file, it automatically tries the secondary. Tesseract failures fall back to Sarvam and vice versa.
- **Hybrid text+OCR handling** — For partially scanned PDFs (some digitally-typed pages, some scanned), the system extracts text from digital pages and OCR's the empty pages, then merges them seamlessly.
- **Tesseract pipeline** — Converts PDF pages to PNG images using `pdftoppm` (from Poppler) or ImageMagick, then runs Tesseract with CPU-core-limited concurrency to prevent thrashing.
- **Sarvam pipeline** — Job-based async API: creates a job, uploads the PDF, starts processing, polls for completion with circuit breaker retry logic, then downloads and parses the result (handles ZIP of per-page markdown files or a single merged document).

### 📁 Project Management
- **Multiple isolated projects** — Each project has its own uploaded files, vector/BM25 indexes, and conversation history. Data is stored under `data/projects/<id>/`.
- **CRUD operations** — Create, list, activate (switch), rename, and delete projects from the sidebar.
- **LRU index cache** — Up to 5 project indexes are cached in memory. Switching between projects with cached indexes is instant; others are loaded from disk on demand.
- **Per-file management** — Individual files can be removed from a project after upload (before or after processing). Removing a processed file also cleans its chunks from the vector and BM25 indexes and re-saves the updated index to disk.

### 💬 Conversation History
- **Persistent conversations** — All Q&A threads are saved as JSON files grouped by project and conversation ID. Messages include role, content, timestamp, and rich metadata.
- **Auto-naming** — New conversations are automatically named from the first question asked (truncated to a readable title).
- **Full message metadata** — Each assistant message stores thinking, documents, pages, footnotes, confidence score, confidence reason, response time, provider, and model used.
- **Conversation CRUD** — Create, list, switch, rename, and delete conversations from the sidebar.

### 📊 Structured Answers with Citations
- **Inline footnotes** — Answers include `[N]` markers linking specific claims to source documents and page numbers.
- **Confidence scoring** — Each answer includes a confidence score (0.0–1.0) with an explanation of why the score was assigned.
- **Rich JSON output** — LLM responses are parsed into a structured format with flexible type handling (handles LLMs that return page numbers as strings vs. integers, multiple content blocks, markdown-wrapped JSON, etc.).
- **Document overviews in context** — All document summaries (title, type, sections, key entities) are prepended to the retrieval context, giving the LLM a complete corpus view for enumeration and cross-reference queries.

### ⚡ Batch Mode
- **Parallel question suite** — Run multiple predefined questions simultaneously using Go goroutines. Each question independently retrieves context and queries the LLM.
- **Timed results** — Batch responses include total execution time for benchmarking against performance targets.

### 🔐 Security
- **Encrypted API key storage** — API keys saved to `data/settings.json` are encrypted using AES-256-GCM with a machine-specific key derived from the hostname and working directory (SHA-256). This prevents casual reading of keys from disk.
- **Path traversal protection** — File deletion endpoints validate filenames using `filepath.Base()` to prevent directory traversal attacks.
- **CORS middleware** — All API responses include appropriate CORS headers for cross-origin access.

### ⚙️ Settings & Configuration
- **Runtime-configurable** — LLM provider, embedding provider, OCR provider, and all API keys can be changed from the settings UI without restarting.
- **Settings persistence** — All settings are saved to `data/settings.json` and automatically loaded on startup, with `.env` values serving as initial defaults.
- **Priority chain** — Settings load order: `.env` file → saved `settings.json` → UI overrides.

### 🎨 Modern Web UI
- **Dark-themed design system** — 53KB+ CSS with a comprehensive dark mode, custom variables, and consistent styling throughout.
- **Modular frontend** — Vanilla JS split into ES modules: `app.js` (init), `state.js` (shared state), `utils.js` (helpers), `ui.js` (DOM rendering), `projects.js`, `conversations.js`, `files.js`, `ingest.js`, and `query.js`.
- **Three-phase workflow** — Upload Phase → Processing Phase → Chat Phase, with smooth transitions and contextual UI.
- **Sidebar navigation** — Project list with conversation sub-list, action buttons for all CRUD operations.
- **Drag-and-drop uploads** — Drop zone with visual feedback and file type validation (PDF, DOCX only).

### 🚀 One-Click Startup
- **Self-provisioning script** (`start.ps1`) — A 7-step PowerShell script that:
  1. Checks for and auto-installs Go (via winget or MSI download)
  2. Copies `.env.example` to `.env` if missing
  3. Downloads and installs Poppler (PDF-to-image converter) and Tesseract OCR to a local `tools/` directory
  4. Creates `data/` directory
  5. Clears port 8080 of any existing processes
  6. Runs `go mod tidy` + builds the server binary
  7. Starts the server and auto-opens the browser

---

## Quick Start

### Prerequisites

- **Windows 10/11** — The startup script is PowerShell-based (the Go server itself is cross-platform)
- **API Keys** — At least one of: OpenAI, Anthropic, or HuggingFace (for LLM and/or embeddings)
- **Go 1.21+** — Auto-installed by `start.ps1` if missing ([go.dev/dl](https://go.dev/dl/))

### Setup

1. **Clone the repository**
   ```bash
   git clone https://github.com/nss-ark/gocognigo.git
   cd gocognigo
   ```

2. **Configure API keys**
   ```bash
   cp .env.example .env
   # Edit .env with your API keys
   ```

3. **Run** (recommended — handles all dependencies automatically)
   ```powershell
   .\start.ps1
   ```

   Or manually:
   ```bash
   go mod tidy
   go build -o gocognigo.exe ./cmd/server/
   ./gocognigo.exe
   ```

4. **Open** [http://localhost:8080](http://localhost:8080) (auto-opens when using `start.ps1`)

### Environment Variables

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `LLM_PROVIDER` | No | `anthropic` | Default LLM provider: `openai`, `anthropic`, or `huggingface` |
| `OPENAI_API_KEY` | Yes* | — | OpenAI API key (used for LLM, embeddings, and document summaries) |
| `ANTHROPIC_API_KEY` | Yes* | — | Anthropic API key |
| `HUGGINGFACE_API_KEY` | Yes* | — | HuggingFace Inference API key |
| `EMBEDDING_PROVIDER` | No | `openai` | Embedding provider: `openai` or `huggingface` |
| `EMBEDDING_API_KEY` | No | Falls back to provider key | Separate API key for embeddings |
| `OCR_PROVIDER` | No | Auto-detect | OCR provider: `tesseract`, `sarvam`, or empty for auto-detection |
| `SARVAM_API_KEY` | No | — | Sarvam Vision API key for cloud OCR |
| `PORT` | No | `8080` | HTTP server port |

*At least one LLM API key is required. An OpenAI key is also recommended for embedding and document summary generation (uses `text-embedding-3-small` and `gpt-4o-mini`).

---

## Architecture

```
GoCognigo/
├── cmd/server/                         # HTTP server & API layer
│   ├── main.go                         # Entry point: env loading, OCR detection, server bootstrap
│   ├── server.go                       # Server struct, LRU cache, request/response types, settings
│   ├── handlers_ingest.go              # Upload, file management, ingestion pipeline (streamed)
│   ├── handlers_query.go               # Single query, batch query, stats, provider listing
│   ├── handlers_project.go             # Project CRUD: create, list, activate, rename, delete
│   ├── handlers_conv.go                # Conversation CRUD: create, list, rename, delete, messages
│   └── handlers_settings.go            # Settings GET/POST with encrypted key persistence
│
├── internal/                           # Core domain packages
│   ├── extractor/                      # Document text extraction
│   │   ├── pdf.go                      # PDF → page-level chunks (with OCR fallback for scanned pages)
│   │   ├── docx.go                     # DOCX → text chunks
│   │   └── ocr.go                      # OCR providers: Tesseract CLI pipeline + Sarvam Vision API
│   │
│   ├── indexer/                        # Chunking, embedding, and indexing
│   │   └── indexer.go                  # ~150-word chunking with parent text, OpenAI/HuggingFace
│   │                                   # embedders, BM25 indexing via Bleve, binary+JSON persistence,
│   │                                   # concurrent batch embedding (200/batch, 6 workers, retry logic)
│   │
│   ├── retriever/                      # Hybrid search engine
│   │   └── retriever.go                # Vector cosine similarity + BM25 → Reciprocal Rank Fusion →
│   │                                   # parent-page deduplication → top-K results
│   │
│   ├── llm/                            # LLM integration layer
│   │   └── llm.go                      # Provider interface + OpenAI, Anthropic, HuggingFace impls
│   │                                   # Chain-of-thought system prompt, structured JSON output,
│   │                                   # flexible response parsing, document summary generation
│   │
│   ├── chat/                           # Project & conversation persistence
│   │   └── session.go                  # ProjectStore: filesystem-backed CRUD for projects,
│   │                                   # conversations, and messages (JSON files)
│   │
│   └── crypto/                         # Security utilities
│       └── crypto.go                   # AES-256-GCM encrypt/decrypt with machine-derived keys
│
├── web/                                # Frontend (vanilla JS SPA)
│   ├── index.html                      # Single-page shell with all panels and modals
│   ├── style.css                       # Dark-mode design system (53KB+)
│   ├── app.js                          # Initialization and event wiring
│   ├── state.js                        # Shared application state
│   ├── utils.js                        # API helpers, formatting utilities
│   ├── ui.js                           # DOM rendering, sidebar, status indicators
│   ├── projects.js                     # Project CRUD UI logic
│   ├── conversations.js                # Conversation management UI
│   ├── files.js                        # File upload, listing, removal UI
│   ├── ingest.js                       # Ingestion progress polling and display
│   └── query.js                        # Chat interface, batch mode, answer rendering
│
├── data/                               # Runtime data (auto-created, gitignored)
│   ├── projects/                       # Per-project storage
│   │   └── <project-id>/
│   │       ├── uploads/                # Uploaded PDF/DOCX files
│   │       ├── bm25.index/             # Bleve BM25 index
│   │       ├── vectors.gob             # Binary vector store (fast load)
│   │       ├── vectors.json            # JSON vector store (fallback)
│   │       └── conversations/          # Per-conversation message JSON files
│   └── settings.json                   # Encrypted user settings
│
├── tools/                              # Auto-installed OCR dependencies
│   ├── Tesseract-OCR/                  # Tesseract binary + language data
│   └── poppler/                        # Poppler (pdftoppm) for PDF → image conversion
│
├── start.ps1                           # One-click startup script (auto-installs everything)
├── .env.example                        # Template environment configuration
├── go.mod / go.sum                     # Go module dependencies
└── TASK_TRACKER.md                     # Development sprint history and backlog
```

### Data Flow

```
┌─────────────┐     ┌──────────────┐     ┌──────────────┐     ┌──────────────┐
│   Upload    │────▶│   Extract    │────▶│    Chunk     │────▶│    Embed     │
│  PDF/DOCX   │     │  (+ OCR)     │     │ (~150 words) │     │  (OpenAI /   │
│             │     │              │     │ + parent pg  │     │  HuggingFace)│
└─────────────┘     └──────────────┘     └──────────────┘     └──────┬───────┘
                                                                     │
                    ┌──────────────┐     ┌──────────────┐           │
                    │   Summarize  │     │    Index     │◀──────────┘
                    │  (GPT-4o-    │     │  Vector +    │
                    │   mini)      │     │  BM25 (Bleve)│
                    └──────────────┘     └──────┬───────┘
                                                │
┌─────────────┐     ┌──────────────┐     ┌──────┴───────┐
│   Answer    │◀────│     LLM      │◀────│   Hybrid     │◀── User Query
│  + Citations│     │  (OpenAI /   │     │   Search     │
│  + Thinking │     │  Anthropic / │     │  (RRF Fusion)│
│  + Score    │     │  HuggingFace)│     │              │
└─────────────┘     └──────────────┘     └──────────────┘
```

---

## API Reference

### Document Management

| Method | Endpoint | Description |
|--------|----------|-------------|
| `POST` | `/api/upload` | Upload PDF/DOCX files (multipart form, max 100MB). Requires `project_id` field. |
| `GET` | `/api/files?project_id=X` | List uploaded files with names and sizes |
| `DELETE` | `/api/files` | Clear all files and indexes for a project |
| `POST` | `/api/files/delete` | Remove a single file (also cleans vector/BM25 index entries) |

### Ingestion Pipeline

| Method | Endpoint | Description |
|--------|----------|-------------|
| `POST` | `/api/ingest` | Start async document processing (extract → chunk → embed → index) |
| `GET` | `/api/ingest/status` | Poll ingestion progress (phase, files done, chunks done, per-file results) |
| `POST` | `/api/ingest/cancel` | Cancel in-progress ingestion |
| `GET` | `/api/index-status` | Check if a project's index is loaded and ready |

### Querying

| Method | Endpoint | Description |
|--------|----------|-------------|
| `POST` | `/api/query` | Ask a question — returns answer with citations, thinking, confidence, footnotes |
| `POST` | `/api/batch` | Run multiple questions in parallel — returns all answers with total time |
| `GET` | `/api/stats?project_id=X` | Index stats: document count, chunk count, readiness, available providers |
| `GET` | `/api/providers` | List available LLM providers and their models (filtered by configured API keys) |

### Projects

| Method | Endpoint | Description |
|--------|----------|-------------|
| `GET` | `/api/chats` | List all projects |
| `POST` | `/api/chats` | Create a new project |
| `POST` | `/api/chats/activate` | Switch active project (loads index from cache or disk) |
| `POST` | `/api/chats/rename` | Rename a project |
| `DELETE` | `/api/chats/delete` | Delete a project and all its data |

### Conversations

| Method | Endpoint | Description |
|--------|----------|-------------|
| `GET` | `/api/conversations?project_id=X` | List conversations for a project |
| `POST` | `/api/conversations` | Create a new conversation |
| `POST` | `/api/conversations/messages` | Get all messages in a conversation |
| `POST` | `/api/conversations/rename` | Rename a conversation |
| `POST` | `/api/conversations/delete` | Delete a conversation |

### Settings

| Method | Endpoint | Description |
|--------|----------|-------------|
| `GET` | `/api/settings` | Get current settings (keys are decrypted for display) |
| `POST` | `/api/settings` | Update settings (keys are encrypted before saving) |

---

## Tech Stack

| Layer | Technology | Purpose |
|-------|-----------|---------|
| **Language** | Go 1.21+ | High-performance backend with native concurrency |
| **HTTP Server** | `net/http` | Standard library HTTP server with CORS middleware |
| **PDF Extraction** | `ledongthuc/pdf` | Text extraction from digital PDFs, page by page |
| **DOCX Extraction** | `nguyenthenguyen/docx` | Text extraction from Word documents |
| **BM25 Search** | [Bleve](https://github.com/blevesearch/bleve) | Full-text keyword search index with TF-IDF scoring |
| **Vector Embeddings** | OpenAI `text-embedding-3-small` / HuggingFace API | Semantic embedding for similarity search |
| **LLM Providers** | OpenAI, Anthropic, HuggingFace Inference | Structured Q&A with chain-of-thought reasoning |
| **Doc Summaries** | OpenAI `gpt-4o-mini` | Lightweight LLM for document metadata generation |
| **OCR (Local)** | Tesseract OCR + Poppler (`pdftoppm`) | Free, local scanned PDF processing |
| **OCR (Cloud)** | Sarvam Vision Document Intelligence | Paid cloud API for high-quality document OCR |
| **Encryption** | AES-256-GCM | Machine-bound API key encryption at rest |
| **Frontend** | Vanilla JS (ES Modules) + CSS | Modular SPA with dark-mode design system |
| **Persistence** | Filesystem (JSON + Gob + Bleve) | Zero-dependency data storage |

---

## Usage Workflow

### 1. Create a Project
Click **"New Project"** in the sidebar to create an isolated workspace.

### 2. Upload Documents
Drag and drop PDF or DOCX files into the upload zone. You can review your files and remove any unnecessary ones before processing.

### 3. Process Documents
Click **"Process Documents"** to start the ingestion pipeline. The system will:
- Extract text from all files (with OCR fallback for scanned PDFs)
- Split text into ~150-word search chunks linked to full parent pages
- Generate vector embeddings via OpenAI or HuggingFace
- Build a BM25 keyword index via Bleve
- Generate LLM-powered document summaries with section mapping

Watch real-time progress with per-file status and a chunk-level progress bar. Cancel anytime if needed.

### 4. Ask Questions
Once processing completes, type questions in the chat interface. Each answer includes:
- **The answer** with inline `[N]` footnote citations
- **Confidence score** (0.0–1.0) with rationale
- **Thinking process** (collapsible) showing the model's step-by-step reasoning
- **Source documents and page numbers** for verification

### 5. Batch Mode
Switch to **Batch Mode** to run a predefined suite of test questions simultaneously against your document corpus. Results include total execution time for benchmarking.

### 6. Switch Providers
Open **Settings** to change the LLM provider/model, embedding provider, or OCR provider at any time. Changes take effect immediately — no restart required.

---

## License

Private — Comply Ark.
