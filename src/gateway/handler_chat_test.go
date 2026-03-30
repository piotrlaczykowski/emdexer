package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/qdrant/go-client/qdrant"
	"google.golang.org/grpc"
)

// mockPointsClientWithResult is a PointsClient that returns one search result with
// path and text payload fields, simulating a non-empty vector search response.
type mockPointsClientWithResult struct {
	qdrant.PointsClient
}

func (m *mockPointsClientWithResult) Search(_ context.Context, _ *qdrant.SearchPoints, _ ...grpc.CallOption) (*qdrant.SearchResponse, error) {
	return &qdrant.SearchResponse{
		Result: []*qdrant.ScoredPoint{
			{
				Id:    &qdrant.PointId{PointIdOptions: &qdrant.PointId_Num{Num: 1}},
				Score: 0.9,
				Payload: map[string]*qdrant.Value{
					"path": {Kind: &qdrant.Value_StringValue{StringValue: "src/test.go"}},
					"text": {Kind: &qdrant.Value_StringValue{StringValue: "some context text about emdexer"}},
				},
			},
		},
	}, nil
}

func (m *mockPointsClientWithResult) Query(_ context.Context, _ *qdrant.QueryPoints, _ ...grpc.CallOption) (*qdrant.QueryResponse, error) {
	return &qdrant.QueryResponse{}, nil
}

func TestChat_LLMAuthError_ReturnsContext(t *testing.T) {
	s := newTestServer()
	s.pointsClient = &mockPointsClientWithResult{}
	s.llmCallFn = func(_ context.Context, _, _ string) (string, error) {
		return "", fmt.Errorf("gemini API 403: PERMISSION_DENIED")
	}

	body := `{"messages":[{"role":"user","content":"what is emdexer?"}]}`
	r := postWithNamespace("/v1/chat/completions", body, []string{"*"})
	w := httptest.NewRecorder()

	s.handleChatCompletions(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 with fallback context when LLM returns 403, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "LLM unavailable") {
		t.Errorf("expected fallback context message in body, got: %s", w.Body.String())
	}
}

func TestChat_LLMAuthError_NoContext_Returns503(t *testing.T) {
	s := newTestServer()
	// mockPointsClient (default) returns 0 results — no fallback context available.
	s.llmCallFn = func(_ context.Context, _, _ string) (string, error) {
		return "", fmt.Errorf("gemini API 403: PERMISSION_DENIED")
	}

	body := `{"messages":[{"role":"user","content":"what is emdexer?"}]}`
	r := postWithNamespace("/v1/chat/completions", body, []string{"*"})
	w := httptest.NewRecorder()

	s.handleChatCompletions(w, r)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503 when LLM returns 403 and no context available, got %d: %s", w.Code, w.Body.String())
	}
}

func TestChat_StreamingIncrementsChunkCounter(t *testing.T) {
	s := newTestServer()
	s.pointsClient = &mockPointsClientWithResult{}
	s.streamEnabled = true
	s.streamCallFn = func(_ context.Context, _, _ string, onChunk func(string) error) error {
		_ = onChunk("tok1")
		_ = onChunk("tok2")
		_ = onChunk("tok3")
		return nil
	}

	before := testutil.ToFloat64(chatStreamChunksTotal)

	body := `{"messages":[{"role":"user","content":"what is emdexer?"}],"stream":true}`
	r := postWithNamespace("/v1/chat/completions", body, []string{"*"})
	w := httptest.NewRecorder()
	s.handleChatCompletions(w, r)

	after := testutil.ToFloat64(chatStreamChunksTotal)
	if after-before != 3 {
		t.Errorf("expected 3 chunk increments, got %.0f", after-before)
	}
}

func TestChat_StreamingTTFTRecordedOnce(t *testing.T) {
	s := newTestServer()
	s.pointsClient = &mockPointsClientWithResult{}
	s.streamEnabled = true
	// Deliver 5 chunks — TTFT must still be observed exactly once.
	s.streamCallFn = func(_ context.Context, _, _ string, onChunk func(string) error) error {
		for i := 0; i < 5; i++ {
			_ = onChunk(fmt.Sprintf("tok%d", i))
		}
		return nil
	}

	body := `{"messages":[{"role":"user","content":"what is emdexer?"}],"stream":true}`
	r := postWithNamespace("/v1/chat/completions", body, []string{"*"})
	w := httptest.NewRecorder()
	s.handleChatCompletions(w, r)

	// Collect the histogram and verify exactly 1 observation was recorded.
	count := testutil.CollectAndCount(chatStreamTTFT)
	if count != 1 {
		t.Errorf("expected TTFT histogram to have exactly 1 observation, got %d", count)
	}
}
