# Research: Emdexer — 2026-03-13

## Question A: chunk_text Storage Opt-Out — Privacy vs UX Tradeoff

### Background

The question is whether to implement a `store_raw_text: false` mode where chunk text is **not** persisted in pgvector — only the embedding vectors are stored, with a back-reference to the file path + byte offset so the text can be re-read from disk on demand.

---

### How Production RAG Systems Handle "No Raw Text"

Most production RAG systems **always store chunk text alongside embeddings**. This is by design:
- Retrieval returns both the vector match AND the text payload, which is then injected into the LLM prompt.
- Without stored text, you cannot assemble context for generation.

However, there is a legitimate use case: **storing only a pointer** (file path + offset) and reconstructing the chunk at query time from the original file. LlamaIndex has an open GitHub Discussion (#13755) where a user explicitly asked how to build exactly this. The LlamaIndex bot's answer was:

> "You can create a TextNode with `text=""` and store metadata with a `source` reference. Implement `get_content()` to retrieve text on-the-fly."

This is possible but **requires a custom node class** — it's not a built-in supported mode.

**LangChain** has no equivalent built-in "no text storage" mode. You'd need to implement a custom `BaseRetriever` that fetches text at retrieval time.

No major OSS RAG framework offers `store_raw_text: false` as a first-class toggle.

---

### Can You Reconstruct Context at Query Time from File?

**Yes, technically** — with the right bookkeeping:
- Store chunk position: `file_path`, `chunk_index`, `byte_offset_start`, `byte_offset_end`
- At query time: open file → seek to offset → read bytes → parse/decode → return text

This works well for:
- Plain text, Markdown, HTML, code files (trivial — raw seek)
- JSON, CSV (simple parsing)

This becomes complex for:
- PDFs (text extraction is not byte-position-stable; a "page 2, paragraph 3" reference is more reliable than byte offsets)
- DOCX/XLSX/PPTX (binary/ZIP formats — you must re-extract and re-chunk, then select the right chunk by index)
- Scanned images with OCR (you'd need to re-run OCR at query time — impractical)

**The reliable approach** is to store chunk index + original file path and re-run the extraction pipeline at retrieval time. But this means the full extraction + chunking pipeline runs on every query hit — which is expensive.

---

### Latency Implications

| Approach | Latency (typical) | Notes |
|---|---|---|
| Stored `chunk_text` in pgvector | ~0–2 ms overhead | Text is part of the row; returned with the embedding match result |
| On-the-fly re-read (plain text) | +5–50 ms | File open + seek; fast for local NVMe, slow for network mounts |
| On-the-fly re-extract (PDF/DOCX) | +200ms–2s | Full re-parsing per hit per query |
| On-the-fly OCR (scanned image) | +2–10s | Completely impractical at query time |

For a **self-hosted semantic search system**, the latency cost of not storing chunk_text ranges from acceptable (plain text files, fast local NVMe) to completely prohibitive (PDFs, images, DOCX). A query returning 5 chunks from 5 different PDFs could add 5–10 seconds of latency per search.

---

### The Real Privacy Benefit — Is It Meaningful?

This is the most important question, and the answer is: **the privacy benefit is marginal and possibly illusory** in a self-hosted context.

**Arguments that it's NOT meaningful:**

1. **You store file paths.** File paths are often as sensitive as their contents (`/home/piotr/taxes-2025/contract-NDA-ExampleCorp.pdf`). A path leak is already a data leak.

2. **Embeddings are NOT anonymized data.** Recent ML research (Vec2Text, 2023; IronCore Labs, 2024; arXiv 2411.05034, 2024; GEIA 2025) demonstrates that text can be substantially recovered from embeddings:
   - Vec2Text achieves **92% perfect reconstruction** for 32-token chunks with iterative refinement.
   - Transferable Embedding Inversion Attacks work without even querying the original model.
   - GEIA (2025) is the first generative attack producing coherent full sentences from embeddings.

   In other words: **embeddings already ARE the text, in a fuzzy but recoverable form.** Omitting `chunk_text` from the DB doesn't protect the content if the embeddings are compromised.

3. **It's self-hosted.** The threat model for a self-hosted system on local storage is: "an attacker has access to the pgvector DB but NOT the filesystem." This is an unusual and narrow threat model. If they have DB access, the file paths give them everything they need to find the original files.

4. **Incremental value over stored paths.** If you're worried about someone having DB access and reading sensitive content, the right solution is full-disk encryption or DB-level encryption — not withholding chunk text.

**Arguments where it COULD matter:**

- If the **DB and filesystem are on separate systems** with separate access controls (e.g., DB on a shared cloud instance, files on local NFS). In this case, not storing chunk text in DB could prevent DB-only attackers from reading file contents. But file paths still leak document names.
- **Regulatory/compliance scenarios** where the data classification policy requires that "raw document content" never enters a database row. Some GDPR/SOC2 interpretations could trigger this.
- **Purge use case**: If you delete a file, any stored chunk text in the DB is now a ghost copy of deleted data. If you DON'T store chunk text, file deletion is automatically complete (modulo embeddings, which are harder to link back).

---

### Recommendation: Is the Opt-Out Worth Implementing?

**No — not as an MVP feature. Yes — as a future flag, if the architecture is designed for it.**

**Pragmatic conclusion:**
- Store `chunk_text` by default. It's needed for good UX (no re-extraction latency, no need to manage file availability at query time).
- The privacy benefit of omitting it is weak given: (a) you store paths, (b) embeddings are partially recoverable, (c) it's self-hosted.
- The **real cost** is implementation complexity: you need to handle re-extraction for all formats at query time, deal with file unavailability (file moved/deleted), and re-run expensive pipelines on every search hit.

**If you want to implement it cleanly:**
- Store: `embedding`, `file_path`, `chunk_index`, `chunk_start_offset`, `chunk_end_offset`, `chunk_hash` (for verification)
- At query time: re-open file, re-extract, select chunk by index
- Limit the feature to **plain text formats only** (TXT, Markdown, HTML, code), where re-read is fast and reliable
- For PDF/DOCX/images: either always store chunk_text, or raise an explicit error when `store_raw_text=false` is requested

**If regulatory compliance is the driver**, consider instead: transparent DB-level encryption (pgcrypto, full-disk encryption at OS level), which solves the actual threat model without crippling UX.

---

## Question B: Apache Tika Alternatives for File Text Extraction in Go

### The Problem with Tika

Apache Tika as a Java sidecar has real operational costs:
- JVM startup: 2–5 seconds cold start
- Memory: 256–512 MB RSS baseline
- Container complexity: separate sidecar, HTTP transport overhead
- Deployment weight for a self-hosted app

That said, Tika's **breadth** (1000+ formats, including legacy `.doc`, `.ppt`, `.msg`, `.odt`, `.pages`, and exotica) is genuinely hard to replicate.

---

### Go-Native Libraries

#### PDF

| Library | GitHub | Notes |
|---|---|---|
| `ledongthuc/pdf` | https://github.com/ledongthuc/pdf | Pure Go, basic text extraction from text-layer PDFs. No OCR. Handles most standard PDFs. Simple API. |
| `dslipak/pdf` | https://github.com/dslipak/pdf | Fork of ledongthuc/pdf, active maintenance as of 2024. Better handling of some malformed PDFs. |
| `pdfcpu` | https://github.com/pdfcpu/pdfcpu | Full PDF toolkit (merge, split, watermark, validate). Text extraction is possible but secondary to its primary manipulation features. Pure Go. Apache 2.0. |
| `unipdf` (UniDoc) | https://github.com/unidoc/unipdf | Comprehensive pure Go PDF library. Excellent text extraction, table parsing, form fields. **Commercial license required** (paid). |
| `heussd/pdftotext-go` | https://github.com/heussd/pdftotext-go | Go wrapper around `pdftotext` (poppler CLI). Not pure Go — requires poppler system package. |

**Practical choice for PDF**: `dslipak/pdf` for simple extractions; `pdfcpu` if you need more robustness. For production-grade complex PDFs, `unipdf` is best but costs money. For the hybrid approach, keep Tika as fallback for encrypted/complex PDFs.

#### DOCX / Office Open XML

| Library | GitHub | Notes |
|---|---|---|
| `fumiama/go-docx` | https://github.com/fumiama/go-docx | Pure Go. Read+write DOCX. Active. AGPL-3.0. Good for text extraction (iterate paragraphs + tables). |
| `sajari/docconv` | https://github.com/sajari/docconv | Go wrapper: DOCX (pure Go XML), PDF (via poppler), DOC (via `wv`), RTF (via `unrtf`), HTML. Mixed: some pure Go, some system deps. MIT. Actively maintained. |

**`sajari/docconv`** is the most pragmatic **Go-native multi-format entry point**: handles DOCX/PDF/HTML/RTF/XML with a single `ConvertPath()` call. PDF extraction relies on `poppler-utils` system package (lightweight vs JVM). OCR support via `gosseract` build tag.

#### XLSX / Spreadsheets

| Library | GitHub | Notes |
|---|---|---|
| `qax-os/excelize` | https://github.com/qax-os/excelize | Pure Go. The gold standard for Go XLSX. Reads all cell values. Streaming API for large files. BSD-3. Actively maintained. |
| `tealeg/xlsx` | https://github.com/tealeg/xlsx | Older, pure Go. Still works but less actively maintained than excelize. |

**Recommendation**: `excelize/v2` — it's the clear winner for XLSX.

#### PPTX

| Library | GitHub | Notes |
|---|---|---|
| `sajari/docconv` (pptx.go) | https://github.com/sajari/docconv/blob/master/pptx.go | Parses slide XML, extracts text bodies. Pure Go. Sufficient for text extraction use case. |
| `manuviswam/go-pptx` | https://github.com/manuviswam/go-pptx | Minimal manipulation library, less suited for extraction. |

No dedicated Go PPTX extraction library exists — `docconv`'s approach (unzip + XML parse slide content types) is the correct pattern.

#### OCR / Images

| Library | GitHub | Notes |
|---|---|---|
| `otiai10/gosseract` | https://github.com/otiai10/gosseract | Go CGO bindings for Tesseract. The standard for Go OCR. Requires `libtesseract` system package. Actively maintained (3k+ stars). |

For OCR, there's no pure-Go alternative worth considering. `gosseract` + Tesseract is the pragmatic choice.

#### Email (.eml / .msg)

| Library | GitHub | Notes |
|---|---|---|
| `DusanKasan/parsemail` | https://github.com/DusanKasan/parsemail | Pure Go .eml parser. Parses RFC5322 headers, body, attachments. |
| `hexiosec/email-parse` | https://github.com/hexiosec/email-parse | Supports both `.msg` (OLE/Outlook format) and `.eml`. The only Go library handling both formats. |
| Go stdlib `net/mail` | — | RFC5322 basics, no attachment handling |

**`.msg` is the hard part**: Outlook MSG files use OLE Compound Document format. `hexiosec/email-parse` is the only known Go library that handles both `.msg` and `.eml`.

---

### Non-Go Alternatives Worth Considering

#### extractous (Rust, with Go bindings)

- **GitHub**: https://github.com/yobix-ai/extractous
- **Go bindings**: https://github.com/rahulpoonia29/extractous-go
- Core written in Rust, uses GraalVM-compiled Tika native libs (no JVM at runtime).
- Supports 60+ file formats (everything Tika supports).
- OCR via Tesseract.
- **Streaming API** for large files (important for memory efficiency).
- Python and upcoming JS/TS bindings.
- The Go bindings use CGO + native `.so` library — requires `LD_LIBRARY_PATH` setup.
- **This is the closest thing to a "drop-in Tika replacement"** — same breadth, no JVM, native speed.

```go
extractor := extractous.New()
content, metadata, err := extractor.ExtractFileToString("document.pdf")
```

⚠️ The Go bindings (`rahulpoonia29/extractous-go`) are community-maintained, not official. Check maturity/activity before committing.

#### docconv (Go + system deps)

Already covered above — the pragmatic multi-format Go library. Handles most common formats. Requires `poppler-utils`, `wv`, `unrtf` system packages but these are small (no JVM).

#### Python sidecar: Kreuzberg (lightweight Tika alternative)

- **GitHub**: https://github.com/Goldziher/kreuzberg
- Python library that wraps `pandoc`, `pdfminer`, `python-docx`, `tesseract`, etc.
- Benchmarked against Docling, MarkItDown, Unstructured across 94 real-world documents.
- **Faster** than Docling (which uses heavy ML models), lighter than Tika.
- Winner in the 2025 community benchmarks for speed + correctness tradeoff.
- Could serve as a Python microservice sidecar — smaller than Tika (no JVM).

#### Python sidecar: MarkItDown (Microsoft)

- **GitHub**: https://github.com/microsoft/markitdown
- Converts PDF, DOCX, PPTX, XLSX, HTML, images → Markdown.
- Extremely popular (50k+ GitHub stars as of 2025).
- Lightweight (pure Python dependencies, no ML models required by default).
- Output is Markdown (structured), not raw text — good for chunking.
- OCR plugin available (uses LLM Vision — cloud only unless running local LLM).

#### Pandoc (subprocess)

- Universal document converter (`pandoc`), callable as a subprocess from Go.
- Handles: DOCX, PPTX, HTML, RTF, RST, ODT, EPUB, LaTeX → plain text or Markdown.
- Does NOT handle PDF (can generate PDF but not extract from it).
- Very fast, small binary, no JVM.
- Good for the "office formats → markdown" pipeline component.

---

### What Do Other Self-Hosted Search Projects Use?

| Project | Extraction approach |
|---|---|
| **Meilisearch** | Does not extract — expects pre-extracted text; users pipe text in via indexing API |
| **Typesense** | Same as Meilisearch — no built-in extraction, expects structured JSON |
| **Perplexica** | Uses SearXNG for web content; no local file extraction |
| **Open WebUI** | Supports Tika or Docling as configurable extractors (toggleable in settings) |
| **txtai** | Uses Apache Tika by default; adding Docling as an alternative (GitHub issue #814) |
| **LlamaIndex** | Pluggable reader system; community uses `SimpleDirectoryReader` with per-format parsers |

The pattern is clear: **Tika is the incumbent** for full-format coverage in self-hosted stacks. Projects moving away from it tend to go to Docling (Python, heavier but better for complex docs) or a hybrid approach.

---

### The Hybrid Approach (Recommended Architecture)

```
┌──────────────────────────────────────────────────────────────────┐
│                    Extraction Dispatcher (Go)                    │
├─────────────────┬────────────────────┬───────────────────────────┤
│  Go-native      │  subprocess        │  Sidecar / fallback       │
│  (pure Go or    │  (no JVM, fast)    │  (for exotic formats)     │
│  CGO)           │                    │                           │
├─────────────────┼────────────────────┼───────────────────────────┤
│ .txt .md .html  │ pandoc → .docx     │ Tika (keep as fallback)   │
│ .json .csv      │ .pptx .odt .rtf    │ OR extractous-go          │
│ .pdf → dslipak  │ pdftotext → .pdf   │ for legacy/exotic formats │
│ .xlsx → excelize│ (poppler)          │ (.doc .ppt .msg .odt etc) │
│ .docx → go-docx │                    │                           │
│ .eml/.msg →     │ tesseract → images │                           │
│ hexiosec/email  │ (OCR)              │                           │
└─────────────────┴────────────────────┴───────────────────────────┘
```

**Format-by-format breakdown:**

| Format | Recommended Go approach | Notes |
|---|---|---|
| `.txt`, `.md`, `.csv`, `.json`, code | stdlib `os.ReadFile` + UTF-8 decode | Trivial |
| `.html` | `golang.org/x/net/html` (stdlib-adjacent) | Strip tags |
| `.pdf` | `dslipak/pdf` for text-layer; `gosseract` for scanned | Fallback to Tika for encrypted/complex |
| `.docx` | `fumiama/go-docx` or `sajari/docconv` | Pure Go XML parsing |
| `.xlsx` | `qax-os/excelize` | Pure Go, mature |
| `.pptx` | `sajari/docconv` pptx.go | Pure Go XML parsing |
| `.eml` | `DusanKasan/parsemail` | Pure Go RFC5322 |
| `.msg` | `hexiosec/email-parse` | OLE format — unique lib |
| images (OCR) | `otiai10/gosseract` + Tesseract | CGO, system dep |
| `.doc`, `.ppt`, `.odt`, `.rtf` | Tika sidecar OR extractous-go | Legacy binary formats |

---

### Recommendation

**Pragmatic choice for a self-hosted Go project:**

#### Primary: `sajari/docconv` + `excelize` + `gosseract`

- `sajari/docconv` handles: PDF (via poppler), DOCX, PPTX, HTML, RTF, XML, DOC (via `wv`)
- `excelize` handles: XLSX
- `gosseract` handles: image OCR
- System deps (APT packages): `poppler-utils wv unrtf tidy` — these are **small** (< 50 MB combined, no JVM)
- Result: covers ~90% of realistic formats with no JVM overhead

#### Secondary / Fallback: extractous-go

If you need true Tika-level breadth (1000+ formats) without JVM:
- Use `extractous-go` as the fallback path for formats not covered by Go-native libs
- It's CGO-based (ships native libs), no Python required
- ⚠️ Community Go bindings — evaluate stability before production adoption

#### Avoid:

- **unipdf**: Commercial license, incompatible with OSS/self-hosted free distribution
- **Docling**: Too heavy for this use case (ML models, slow startup)
- **Tika sidecar as primary**: Acceptable as fallback-only to handle 10% exotic formats

#### Implementation path:

1. Start with `sajari/docconv` + `excelize` — covers 80% of formats with minimal code.
2. Add `gosseract` for image OCR (build tag to keep it optional).
3. Add `hexiosec/email-parse` for `.eml`/`.msg`.
4. Evaluate `extractous-go` for legacy binary formats (`.doc`, `.ppt`, `.odt`).
5. Keep Tika as optional last-resort fallback only if exotic formats become a real need.

---

*Researched: 2026-03-13 by Kiro (subagent)*
