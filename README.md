# GoCognigo

**High-performance document reasoning engine** built in Go with a hybrid RAG (Retrieval-Augmented Generation) pipeline, multi-provider LLM support, and a modern dark-themed web UI.

Upload PDFs and DOCX files, let the engine chunk, index, and embed them, then ask natural-language questions and receive structured, cited answers.

---

## Features

- **Upload-first workflow** — Drag-and-drop PDF/DOCX files, process them, then chat
- **Hybrid search** — BM25 (keyword) + vector similarity for high-recall retrieval
- **Multi-provider LLM** — OpenAI, Anthropic, and HuggingFace with runtime switching
- **Multi-provider embeddings** — OpenAI or HuggingFace embedding models
- **OCR support** — Tesseract (local/free) and Sarvam Vision (cloud API) for scanned PDFs
- **Project management** — Multiple projects with separate document sets and conversations
- **Conversation history** — Persistent chat history with auto-naming
- **Batch mode** — Run a suite of test questions and compare results
- **Settings persistence** — API keys and preferences saved across restarts
- **Individual file management** — Remove uploaded files before processing

## Architecture

```
cmd/server/main.go          HTTP server, API handlers, shared state
internal/
  chat/session.go           Project & conversation persistence (JSON/filesystem)
  extractor/
    pdf.go                  PDF text extraction (ledongthuc/pdf)
    docx.go                 DOCX text extraction (nguyenthenguyen/docx)
    ocr.go                  OCR fallback (Tesseract CLI + Sarvam Vision API)
  indexer/indexer.go        Chunking, embedding (OpenAI/HuggingFace), BM25 index (Bleve)
  retriever/retriever.go    Hybrid search: vector cosine similarity + BM25 fusion
  llm/llm.go               LLM providers: OpenAI, Anthropic, HuggingFace
web/
  index.html                Single-page app shell
  app.js                    Frontend logic (vanilla JS)
  style.css                 Dark-mode design system
data/                       Runtime data (auto-created)
  projects/                 Per-project uploads, indexes, conversations
  settings.json             Persisted user settings
```

## Quick Start

### Prerequisites

- **Go 1.21+** — [go.dev/dl](https://go.dev/dl/)
- **API Keys** — At least one of: OpenAI, Anthropic, or HuggingFace
- **Optional**: Tesseract OCR for scanned PDF support

### Setup

1. **Clone and configure**
   ```bash
   git clone <repo-url>
   cd GoCognigo
   cp .env.example .env
   # Edit .env with your API keys
   ```

2. **Run with the startup script** (recommended)
   ```powershell
   .\start.ps1
   ```

   Or manually:
   ```bash
   go run cmd/server/main.go
   ```

3. **Open** [http://localhost:8080](http://localhost:8080)

### Environment Variables

| Variable | Required | Description |
|----------|----------|-------------|
| `LLM_PROVIDER` | No | Default LLM: `openai`, `anthropic`, or `huggingface` (default: `openai`) |
| `OPENAI_API_KEY` | Yes* | OpenAI API key (used for LLM and/or embeddings) |
| `ANTHROPIC_API_KEY` | Yes* | Anthropic API key |
| `HUGGINGFACE_API_KEY` | Yes* | HuggingFace API key |
| `EMBEDDING_PROVIDER` | No | `openai` or `huggingface` (default: `openai`) |
| `EMBEDDING_API_KEY` | No | Separate embedding key; falls back to provider key |
| `OCR_PROVIDER` | No | `tesseract` or `sarvam` for scanned PDFs |
| `SARVAM_API_KEY` | No | Sarvam Vision API key (if using Sarvam OCR) |
| `PORT` | No | Server port (default: `8080`) |

*At least one LLM API key is required.

## Usage

1. **Create a project** — Click "New Project" in the sidebar
2. **Upload documents** — Drag & drop PDFs or DOCX files into the upload zone
3. **Remove files** (optional) — Click × next to any file to remove it before processing
4. **Process** — Click "Process Documents" to extract, chunk, and embed
5. **Query** — Ask questions in the chat interface; answers include citations with page numbers
6. **Batch mode** — Switch to Batch Mode to run a predefined question suite

## API Endpoints

| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/api/stats` | Index stats (documents, chunks, readiness) |
| GET | `/api/providers` | Available LLM providers and models |
| GET/POST | `/api/settings` | Read or update server settings |
| POST | `/api/upload` | Upload files (multipart form) |
| GET/DELETE | `/api/files` | List or clear all uploaded files |
| POST | `/api/files/delete` | Remove a single uploaded file |
| POST | `/api/ingest` | Start document processing pipeline |
| GET | `/api/ingest/status` | Poll processing progress |
| POST | `/api/ingest/cancel` | Cancel in-progress processing |
| POST | `/api/query` | Ask a question (single query) |
| POST | `/api/batch` | Run batch question suite |
| GET/POST | `/api/chats` | List or create projects |
| POST | `/api/chats/activate` | Switch active project |
| DELETE | `/api/chats/delete` | Delete a project |
| POST | `/api/chats/rename` | Rename a project |
| GET/POST | `/api/conversations` | List or create conversations |
| POST | `/api/conversations/delete` | Delete a conversation |
| POST | `/api/conversations/messages` | Get conversation messages |
| POST | `/api/conversations/rename` | Rename a conversation |

## Tech Stack

| Component | Technology |
|-----------|-----------|
| Backend | Go, net/http |
| PDF extraction | ledongthuc/pdf |
| DOCX extraction | nguyenthenguyen/docx |
| BM25 search | Bleve |
| Vector embeddings | OpenAI / HuggingFace APIs |
| LLM | OpenAI / Anthropic / HuggingFace APIs |
| OCR | Tesseract CLI / Sarvam Vision API |
| Frontend | Vanilla JS, CSS (dark mode) |
| Data persistence | Filesystem (JSON + Bleve index) |

## License

Private — Comply Ark.
