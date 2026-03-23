# Multi-Modal Hardening (Phase 26)

Emdexer supports three opt-in extraction tracks for audio, image, and video content. All tracks are **disabled by default** — the standard text/document pipeline is unchanged until you explicitly enable them.

---

## Track 1 — Whisper (Audio/Video Transcription)

The Whisper track sends audio and video files to a local [whisper.cpp](https://github.com/ggerganov/whisper.cpp) sidecar for transcription. No tokens are spent — all inference runs locally.

### Supported formats

`.mp3`, `.wav`, `.m4a`, `.ogg`, `.flac` (audio) and `.mp4`, `.mkv`, `.avi`, `.mov`, `.webm` (video audio track)

### Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `EMDEX_WHISPER_ENABLED` | `false` | Master toggle — must be `true` to transcribe |
| `EMDEX_WHISPER_URL` | — | Whisper sidecar address, e.g. `http://whisper:8080` |
| `EMDEX_WHISPER_MODEL` | `base` | Model name: `base`, `small`, `medium`, `large` |
| `EMDEX_WHISPER_MIN_CHARS` | `50` | Skip transcripts shorter than N characters |
| `EMDEX_WHISPER_LANGUAGE` | — | Optional language hint (e.g. `en`, `fr`) |

### Hardening features

- **Retry on 503**: transient sidecar unavailability is retried up to 3× with exponential back-off (1 s, 2 s, 4 s).
- **Quality filter**: transcripts below `EMDEX_WHISPER_MIN_CHARS` are silently discarded.
- **Language hint**: setting `EMDEX_WHISPER_LANGUAGE` improves accuracy for non-English content.
- **Segment metadata**: timed segments `[{"start": 0.0, "end": 5.2, "text": "..."}]` are stored in the Qdrant point payload as `segments`.

### Docker Compose

The Whisper service is defined in `docker-compose.yml`. Uncomment the env var block in the `node` service:

```yaml
- EMDEX_WHISPER_ENABLED=true
- EMDEX_WHISPER_URL=http://whisper:8080
```

### Prometheus metrics

| Metric | Description |
|--------|-------------|
| `emdexer_node_whisper_retries_total` | HTTP 503 retry count |
| `emdexer_node_whisper_skipped_short_total` | Transcripts discarded for being too short |
| `emdexer_node_audio_skipped_total` | Audio files skipped because `EMDEX_WHISPER_ENABLED=false` |

---

## Track 2 — Gemini Vision (Image Captioning)

The Vision track sends image files to the Gemini Vision API to generate rich text descriptions. It uses the same `GOOGLE_API_KEY` and `EMDEX_LLM_MODEL` already configured for the gateway.

**Vision takes priority over OCR when both are enabled.**

### Supported formats

`.png`, `.jpg`, `.jpeg`, `.tiff`, `.bmp`

### Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `EMDEX_VISION_ENABLED` | `false` | Master toggle |
| `EMDEX_VISION_MAX_SIZE_MB` | `10` | Skip images larger than this (MB) |

The API key is taken from `GOOGLE_API_KEY`. The model is taken from `EMDEX_LLM_MODEL` (default: `gemini-3-flash-preview`).

### Payload

When Vision is used, two additional fields are written to the Qdrant point:

```json
{
  "text": "<generated caption>",
  "extraction_method": "gemini-vision"
}
```

### Routing logic

```
Image file (.png / .jpg / .jpeg / .tiff / .bmp)
  └─ EMDEX_VISION_ENABLED=true  → Gemini Vision caption  ← takes priority
  └─ EMDEX_ENABLE_OCR=true      → Extractous + Tesseract OCR
  └─ neither                    → file skipped (error logged)
```

### Prometheus metrics

| Metric | Labels | Description |
|--------|--------|-------------|
| `emdexer_node_vision_calls_total` | `status` (`ok`/`error`/`skipped_size`) | Vision API call outcomes |
| `emdexer_node_vision_duration_ms` | — | Vision API latency histogram (ms) |

---

## Track 3 — FFmpeg (Video Frame Extraction)

The Frame track submits video files to a lightweight Python sidecar (`src/ffmpeg-sidecar/`) that uses FFmpeg to extract JPEG frames at a configurable interval. When Gemini Vision is also enabled, each frame is captioned and the results are concatenated into the indexed text.

### Supported formats

`.mp4`, `.mkv`, `.avi`, `.mov`, `.webm`

### FFmpeg sidecar

```
src/ffmpeg-sidecar/
├── Dockerfile        # python:3.14-slim + ffmpeg
├── server.py         # FastAPI: GET /health, POST /frames
└── requirements.txt
```

**API**:
- `GET /health` → `{"status": "ok"}`
- `POST /frames?interval=30&max_frames=10` — multipart video upload → `{"frames": [{"timestamp_sec": 30, "data": "<base64 JPEG>"}]}`

### Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `EMDEX_FRAME_ENABLED` | `false` | Master toggle |
| `EMDEX_FFMPEG_URL` | — | FFmpeg sidecar address, e.g. `http://ffmpeg-sidecar:8004` |
| `EMDEX_FRAME_INTERVAL_SEC` | `30` | Seconds between extracted frames |
| `EMDEX_MAX_FRAMES` | `10` | Maximum frames per video |

### Video routing logic

```
Video file (.mp4 / .mkv / .avi / .mov / .webm)
  ├─ EMDEX_WHISPER_ENABLED=true → transcribe audio track
  ├─ EMDEX_FRAME_ENABLED=true   → extract frames
  │     └─ EMDEX_VISION_ENABLED=true → caption each frame via Gemini Vision
  └─ neither → file skipped
```

Frame failure never blocks audio transcription. Both run independently and their results are combined.

### Docker Compose

Add the FFmpeg sidecar to `docker-compose.yml` by uncommenting:

```yaml
# ffmpeg-sidecar:
#   build: ../../src/ffmpeg-sidecar
#   networks:
#     - emdexer-net
#   restart: unless-stopped
#   deploy:
#     resources:
#       limits:
#         cpus: "2"
#         memory: 512M
```

And in the `node` service:

```yaml
# - EMDEX_FRAME_ENABLED=true
# - EMDEX_FFMPEG_URL=http://ffmpeg-sidecar:8004
```

### Prometheus metrics

| Metric | Description |
|--------|-------------|
| `emdexer_node_frames_extracted_total` | Total frames extracted across all videos |
| `emdexer_node_frames_duration_ms` | FFmpeg sidecar call latency histogram (ms) |
| `emdexer_node_video_skipped_total` | Videos skipped because no extractor was enabled |

---

## Combining tracks

All three tracks can run simultaneously on a video file. A transcribed MP4 with frame captions produces an indexed document like:

```
<whisper transcript>

[Frame at 0s] A slide showing quarterly revenue charts with an upward trend.
[Frame at 30s] A diagram of the system architecture with three microservices.
[Frame at 60s] A table of API endpoints with response time benchmarks.
```

The `segments` field on the Qdrant point carries whisper's timed segment JSON for downstream use.
