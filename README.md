<div align="center">

# GoCognigo

**High-Performance Document Intelligence Engine**

*Ingest hundreds of documents. Ask complex questions. Get cited answers in seconds.*

[![Go](https://img.shields.io/badge/Go-1.21+-00ADD8?style=for-the-badge&logo=go&logoColor=white)](https://go.dev)
[![License](https://img.shields.io/badge/License-Private-red?style=for-the-badge)](LICENSE)

[Features](#-features) · [Screenshots](#-screenshots) · [Quick Start](#-quick-start) · [Architecture](#-architecture) · [API Reference](#-api-reference)

</div>

---

## Overview

GoCognigo is a document intelligence engine built in Go that combines **hybrid retrieval** (BM25 + vector search) with **multi-provider LLM reasoning** to answer complex questions across large document corpora. It handles PDFs (including scanned), DOCX files, and supports OCR — all through a modern dark-themed web interface.

```mermaid
graph LR
    A[📄 Upload Documents] --> B[🔍 Extract & Chunk]
    B --> C[🧮 Embed & Index]
    C --> D[💬 Ask Questions]
    D --> E[✅ Cited Answers]

    style A fill:#1a1a2e,stroke:#7c3aed,color:#e0e0e0
    style B fill:#1a1a2e,stroke:#7c3aed,color:#e0e0e0
    style C fill:#1a1a2e,stroke:#7c3aed,color:#e0e0e0
    style D fill:#1a1a2e,stroke:#7c3aed,color:#e0e0e0
    style E fill:#1a1a2e,stroke:#22c55e,color:#e0e0e0
```

---

## ✨ Features

### Hybrid RAG Pipeline

```mermaid
flowchart TB
    Q[User Query] --> EMB[Embed Query]
    EMB --> VS[Vector Search<br/>Cosine Similarity]
    EMB --> BM[BM25 Search<br/>Keyword Matching]
    VS --> RRF[Reciprocal Rank Fusion<br/>k=60]
    BM --> RRF
    RRF --> DD[Parent-Page Deduplication]
    DD --> CTX[Build Context<br/>Doc Summaries + Top-20 Results]
    CTX --> LLM[LLM Reasoning<br/>Chain-of-Thought]
    LLM --> ANS[Structured Answer<br/>Citations + Confidence]

    style Q fill:#7c3aed,stroke:#7c3aed,color:#fff
    style RRF fill:#2563eb,stroke:#2563eb,color:#fff
    style LLM fill:#059669,stroke:#059669,color:#fff
    style ANS fill:#22c55e,stroke:#22c55e,color:#fff
```

- **Dual-index retrieval** — BM25 keyword search (Bleve) + vector cosine similarity working together
- **Reciprocal Rank Fusion** — Merges ranked results without needing score calibration
- **Parent-page context** — Small chunks (~150 words) for precise retrieval; full parent pages sent to LLM for rich reasoning

### Multi-Provider LLM

| Provider | Models | Use Case |
|----------|--------|----------|
| **Anthropic** | Claude Opus 4.6, Sonnet, Haiku | Best structured output compliance |
| **OpenAI** | GPT-4o, o-series reasoning | Fastest, reasoning model support |
| **HuggingFace** | Qwen 2.5 72B, Llama, Phi-4 | Open-source, free tier |

Switch providers and models at runtime from the UI — no restart required.

### Chain-of-Thought Reasoning

Every answer includes a collapsible reasoning trace showing the LLM's step-by-step analysis, inline `[N]` footnote citations linking to specific documents and pages, and a confidence score (0.0–1.0) with explanation.

### Document Processing

```mermaid
flowchart LR
    subgraph Extract
        PDF[PDF Parser] --> OCR{Scanned?}
        OCR -->|Yes| TES[Tesseract<br/>Local/Free]
        OCR -->|Yes| SAR[Sarvam Vision<br/>Cloud API]
        OCR -->|No| TXT[Digital Text]
        DOCX[DOCX Parser]
    end
    subgraph Index
        CHK[Chunk ~150 words] --> EMB[Embed<br/>OpenAI / HF]
        EMB --> VEC[Vector Store]
        CHK --> BLV[BM25 Index<br/>Bleve]
        SUM[Doc Summaries<br/>GPT-4o-mini]
    end
    TES --> CHK
    SAR --> CHK
    TXT --> CHK
    DOCX --> CHK

    style TES fill:#22c55e,stroke:#22c55e,color:#fff
    style SAR fill:#3b82f6,stroke:#3b82f6,color:#fff
```

- **PDF & DOCX** extraction with page-level chunking
- **Smart OCR fallback** — Auto-detects scanned pages, tries Tesseract first, falls back to Sarvam Vision
- **LLM-generated summaries** — Structured metadata (title, type, sections, key entities) for each document
- **Concurrent pipeline** — 4 extractors, 6 embedding workers, all stages overlap in time

### Project Management

- **Isolated projects** — Each project has its own files, indexes, and conversations
- **LRU cache** — Up to 5 project indexes held in memory for instant switching
- **Per-file management** — Add or remove individual files, even after processing
- **Persistent conversations** — Auto-named, with full message metadata (thinking, sources, model, timing)

### Batch Evaluation

Run multiple questions in parallel — total time equals the slowest single query, not the sum. Built for benchmarking throughput against time budgets.

### Security

- **AES-256-GCM** encryption for API keys at rest (machine-derived key)
- **Path traversal protection** on all file operations
- **Graceful shutdown** with context cancellation propagation

---

## 📸 Screenshots

### Upload Phase
Drag-and-drop interface for PDF and DOCX files with project sidebar navigation.

<img src="docs/screenshots/01_upload_phase.png" alt="Upload Phase" width="100%"/>

### Chat Interface
Answers with inline footnote citations, source document links with page numbers, confidence scoring, and response timing.

<img src="docs/screenshots/03_chat_interface.png" alt="Chat Interface" width="100%"/>

### Chain-of-Thought Reasoning
Expandable reasoning trace showing the LLM's step-by-step analysis before arriving at the answer.

<img src="docs/screenshots/04_reasoning_expanded.png" alt="Reasoning Expanded" width="100%"/>

### Indexed Documents
Expandable panel showing all ingested documents with type badges (PDF/DOCX).

<img src="docs/screenshots/02_indexed_files.png" alt="Indexed Files" width="100%"/>

### Batch Mode
Load multiple questions and run them simultaneously against the corpus with a time budget indicator.

<img src="docs/screenshots/05_batch_mode.png" alt="Batch Mode" width="100%"/>

### Settings
Runtime configuration for LLM providers, embedding providers, API keys, and OCR with status indicators.

<img src="docs/screenshots/06_settings_panel.png" alt="Settings Panel" width="100%"/>

---

## 🚀 Quick Start

### Prerequisites

- **Go 1.21+** — Auto-installed by `start.ps1` if missing
- **API Keys** — At least one of: OpenAI, Anthropic, or HuggingFace

### Setup

```bash
# Clone
git clone https://github.com/nss-ark/gocognigo.git
cd gocognigo

# Configure
cp .env.example .env
# Edit .env with your API keys

# Run (auto-installs Go, Tesseract, Poppler if missing)
# On Windows:
.\start.ps1
# On Linux/macOS:
chmod +x start.sh && ./start.sh
```

Or manually:
```bash
go mod tidy
go build -o gocognigo.exe ./cmd/server/
./gocognigo.exe
```

Open [http://localhost:8080](http://localhost:8080)

### Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `LLM_PROVIDER` | `anthropic` | Default LLM: `openai`, `anthropic`, `huggingface` |
| `OPENAI_API_KEY` | — | Required for embeddings and document summaries |
| `ANTHROPIC_API_KEY` | — | Anthropic Claude access |
| `HUGGINGFACE_API_KEY` | — | HuggingFace Inference API |
| `EMBEDDING_PROVIDER` | `openai` | `openai` or `huggingface` |
| `OCR_PROVIDER` | auto-detect | `tesseract`, `sarvam`, or empty |
| `SARVAM_API_KEY` | — | Sarvam Vision cloud OCR |
| `PORT` | `8080` | HTTP server port |

> **Note:** Settings can also be changed at runtime from the UI settings panel, persisted to `data/settings.json`.

---

## 🏗 Architecture

### Project Structure

```
GoCognigo/
├── cmd/server/                    # HTTP server & API layer
│   ├── main.go                    # Entry point, env loading, route setup
│   ├── server.go                  # Server struct, LRU cache, settings
│   ├── handlers_ingest.go         # Upload, ingestion pipeline
│   ├── handlers_query.go          # Query, batch, stats, providers
│   ├── handlers_project.go        # Project CRUD
│   ├── handlers_conv.go           # Conversation CRUD
│   └── handlers_settings.go       # Settings with encrypted persistence
│
├── internal/
│   ├── extractor/                 # PDF, DOCX, OCR extraction
│   ├── indexer/                   # Chunking, embedding, BM25+vector indexing
│   ├── retriever/                 # Hybrid search with RRF
│   ├── llm/                       # Multi-provider LLM integration
│   ├── chat/                      # Project & conversation persistence
│   └── crypto/                    # AES-256-GCM encryption
│
├── web/                           # Vanilla JS SPA (ES Modules)
├── docs/screenshots/              # UI screenshots
├── start.ps1                      # One-click startup script
└── data/                          # Runtime data (gitignored)
```

### Ingestion Pipeline

```mermaid
flowchart TB
    subgraph "Concurrent Extraction (4 workers)"
        F1[File 1] --> E1[Extract]
        F2[File 2] --> E2[Extract]
        F3[File 3] --> E3[Extract]
        F4[File N] --> E4[Extract]
    end

    E1 --> CHK[Chunker<br/>~150 words per chunk]
    E2 --> CHK
    E3 --> CHK
    E4 --> CHK

    CHK --> EMB[Concurrent Embedding<br/>6 workers × 200 chunks/batch]

    subgraph "Dual Index"
        EMB --> VEC[(Vector Store<br/>Binary + JSON)]
        EMB --> BM[(BM25 Index<br/>Bleve)]
    end

    subgraph "Async Summaries"
        E1 --> SUM[GPT-4o-mini<br/>Structured Metadata]
        E2 --> SUM
    end

    style CHK fill:#7c3aed,stroke:#7c3aed,color:#fff
    style EMB fill:#2563eb,stroke:#2563eb,color:#fff
    style VEC fill:#059669,stroke:#059669,color:#fff
    style BM fill:#059669,stroke:#059669,color:#fff
    style SUM fill:#d97706,stroke:#d97706,color:#fff
```

All stages run concurrently — embedding starts before extraction finishes through a streamed pipeline architecture.

### Query Flow

```mermaid
sequenceDiagram
    participant U as User
    participant S as Server
    participant R as Retriever
    participant L as LLM Provider

    U->>S: POST /api/query
    S->>R: Embed query + Hybrid search
    R->>R: Vector cosine similarity (top 3K)
    R->>R: BM25 keyword search (top 3K)
    R->>R: Reciprocal Rank Fusion (k=60)
    R->>R: Parent-page deduplication
    R-->>S: Top-20 results + parent pages
    S->>S: Build context (summaries + excerpts)
    S->>L: Send prompt with chain-of-thought
    L-->>S: JSON response (answer, footnotes, confidence, thinking)
    S->>S: Parse + persist to conversation
    S-->>U: Structured answer with citations
```

### Data Storage

```mermaid
graph TB
    subgraph "data/"
        SET[settings.json<br/>Encrypted API keys]
        subgraph "projects/&lt;id&gt;/"
            UP[uploads/<br/>PDF & DOCX files]
            VEC[vectors.gob<br/>Binary vector store]
            VECJ[vectors.json<br/>JSON fallback]
            BM[bm25.index/<br/>Bleve index]
            subgraph "conversations/"
                C1[conv-1.json]
                C2[conv-2.json]
            end
        end
    end

    style SET fill:#ef4444,stroke:#ef4444,color:#fff
    style VEC fill:#059669,stroke:#059669,color:#fff
    style BM fill:#3b82f6,stroke:#3b82f6,color:#fff
```

---

## 📡 API Reference

### Documents & Ingestion

| Method | Endpoint | Description |
|--------|----------|-------------|
| `POST` | `/api/upload` | Upload PDF/DOCX files (multipart, max 100MB) |
| `GET` | `/api/files?project_id=X` | List uploaded files |
| `DELETE` | `/api/files` | Clear all files and indexes |
| `POST` | `/api/files/delete` | Remove a single file + its index entries |
| `POST` | `/api/ingest` | Start ingestion pipeline |
| `GET` | `/api/ingest/status` | Poll ingestion progress |
| `POST` | `/api/ingest/cancel` | Cancel in-progress ingestion |
| `GET` | `/api/index-status` | Check index readiness |

### Querying

| Method | Endpoint | Description |
|--------|----------|-------------|
| `POST` | `/api/query` | Single question → cited answer |
| `POST` | `/api/batch` | Parallel questions → all answers + total time |
| `GET` | `/api/stats?project_id=X` | Index stats (docs, chunks, providers) |
| `GET` | `/api/providers` | Available LLM models per provider |

### Projects & Conversations

| Method | Endpoint | Description |
|--------|----------|-------------|
| `GET` | `/api/chats` | List projects |
| `POST` | `/api/chats` | Create project |
| `POST` | `/api/chats/activate` | Switch active project |
| `POST` | `/api/chats/rename` | Rename project |
| `DELETE` | `/api/chats/delete` | Delete project + all data |
| `GET` | `/api/conversations?project_id=X` | List conversations |
| `POST` | `/api/conversations` | Create conversation |
| `POST` | `/api/conversations/messages` | Get messages |
| `POST` | `/api/conversations/rename` | Rename conversation |
| `POST` | `/api/conversations/delete` | Delete conversation |

### Settings

| Method | Endpoint | Description |
|--------|----------|-------------|
| `GET` | `/api/settings` | Get current settings |
| `POST` | `/api/settings` | Update settings (keys encrypted on save) |

---

## 🛠 Tech Stack

| Layer | Technology | Purpose |
|-------|-----------|---------|
| Language | **Go 1.21+** | Native concurrency, single-binary deploy |
| BM25 | **Bleve** | Pure Go full-text search |
| Embeddings | **OpenAI / HuggingFace** | 1536-dim vector embeddings |
| LLM | **Anthropic / OpenAI / HuggingFace** | Multi-provider structured QA |
| PDF | **ledongthuc/pdf** | Pure Go PDF parsing |
| DOCX | **nguyenthenguyen/docx** | Word document extraction |
| OCR | **Tesseract + Sarvam Vision** | Scanned PDF processing |
| Encryption | **AES-256-GCM** | API key protection at rest |
| Frontend | **Vanilla JS (ES Modules)** | Zero-framework SPA |
| Persistence | **Filesystem (Gob + JSON + Bleve)** | No database required |

---

## 📊 Performance

| Metric | Value |
|--------|-------|
| Concurrent extraction workers | 4 goroutines |
| Embedding batch size | 200 chunks/call |
| Concurrent embedding workers | 6 goroutines |
| Index cache | 5 projects (LRU) |
| Batch query parallelism | All questions concurrent |
| Typical query time (single) | 3–8s |
| Typical batch (15 questions) | 5–12s total |

---

<div align="center">

**Comply Ark (NSS-ARK)**

</div>
