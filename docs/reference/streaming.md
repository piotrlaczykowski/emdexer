# LLM Streaming Configuration

## Overview

Emdexer supports OpenAI-compatible Server-Sent Events (SSE) streaming on the
`POST /v1/chat/completions` endpoint (`"stream": true`).

**Phase 37** replaced the previous fake stream (word-walking a completed response)
with true token-level streaming via Gemini's `streamGenerateContent?alt=sse` endpoint.
Clients now receive tokens as the model generates them, reducing time-to-first-token
from full generation latency to typically 50–500 ms.

## Environment Variable

| Variable | Default | Description |
|----------|---------|-------------|
| `EMDEX_STREAM_ENABLED` | `true` | Enable true LLM token streaming. Set `false` to revert to the deprecated word-walk fake stream. |

## Metrics

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `emdexer_gateway_llm_stream_ttft_ms` | Histogram | `model` | Time-to-first-token in milliseconds |
| `emdexer_gateway_llm_stream_chunks_total` | Counter | `model` | Total token chunks received from streaming |

## Behaviour Details

- The streaming path bypasses the blocking `CallGemini` call — no full response is
  buffered before writing begins.
- If the Gemini stream errors mid-generation, the SSE connection is closed without
  `data: [DONE]`. Standard SSE clients detect this via connection close.
- The `X-Accel-Buffering: no` header disables nginx proxy buffering.
- The HTTP write deadline is cleared (`http.ResponseController.SetWriteDeadline(time.Time{})`)
  so long generations are not killed by the server's write timeout.
- The full accumulated response is written to the audit log after the stream completes.

## Fallback (deprecated)

Set `EMDEX_STREAM_ENABLED=false` to revert to the pre-Phase-37 behaviour: the
gateway waits for the complete LLM response, then word-walks it as SSE chunks.
This is provided for rollback only and will be removed in a future release.
