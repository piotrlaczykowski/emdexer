package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type ollamaChatRequest struct {
	Model    string          `json:"model"`
	Messages []ollamaMessage `json:"messages"`
	Stream   bool            `json:"stream"`
}

type ollamaMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ollamaChatResponse struct {
	Choices []ollamaChoice `json:"choices"`
}

type ollamaChoice struct {
	Message ollamaMessage `json:"message"`
	Delta   ollamaMessage `json:"delta"`
}

// CallOllama performs a non-streaming chat completion via Ollama's OpenAI-compatible API.
// ollamaURL is the base URL e.g. "http://localhost:11434".
// model is the Ollama model name e.g. "gemma4:26b".
func CallOllama(ctx context.Context, prompt, ollamaURL, model string) (string, error) {
	reqBody := ollamaChatRequest{
		Model:    model,
		Messages: []ollamaMessage{{Role: "user", Content: prompt}},
		Stream:   false,
	}
	b, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("ollama: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		ollamaURL+"/v1/chat/completions", bytes.NewReader(b))
	if err != nil {
		return "", fmt.Errorf("ollama: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("ollama: request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("ollama API %d: %s", resp.StatusCode, string(body))
	}

	var cr ollamaChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&cr); err != nil {
		return "", fmt.Errorf("ollama: decode response: %w", err)
	}
	if len(cr.Choices) == 0 {
		return "", fmt.Errorf("ollama: no choices in response")
	}
	return cr.Choices[0].Message.Content, nil
}

// CallOllamaStream streams tokens from Ollama's OpenAI-compatible SSE endpoint.
// Calls onChunk for each content token. Returns on stream end or error.
func CallOllamaStream(ctx context.Context, prompt, ollamaURL, model string, onChunk func(string) error) error {
	reqBody := ollamaChatRequest{
		Model:    model,
		Messages: []ollamaMessage{{Role: "user", Content: prompt}},
		Stream:   true,
	}
	b, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("ollama stream: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		ollamaURL+"/v1/chat/completions", bytes.NewReader(b))
	if err != nil {
		return fmt.Errorf("ollama stream: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("ollama stream: request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("ollama stream API %d: %s", resp.StatusCode, string(body))
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 128*1024), 256*1024)

	for scanner.Scan() {
		line := scanner.Text()
		data, ok := strings.CutPrefix(line, "data: ")
		if !ok || data == "" {
			continue
		}
		if data == "[DONE]" {
			break
		}

		var cr ollamaChatResponse
		if err := json.Unmarshal([]byte(data), &cr); err != nil {
			continue // skip malformed lines
		}
		if len(cr.Choices) == 0 {
			continue
		}
		if text := cr.Choices[0].Delta.Content; text != "" {
			if err := onChunk(text); err != nil {
				return fmt.Errorf("ollama stream: chunk callback: %w", err)
			}
		}
	}

	return scanner.Err()
}
