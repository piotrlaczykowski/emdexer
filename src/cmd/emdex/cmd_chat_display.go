package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/piotrlaczykowski/emdexer/ui"
)

// printStreamedChatResponse reads an SSE stream from body and prints tokens as they arrive.
func printStreamedChatResponse(body io.Reader) {
	scanner := bufio.NewScanner(body)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}
		if len(chunk.Choices) > 0 {
			fmt.Print(chunk.Choices[0].Delta.Content)
		}
	}
	fmt.Println()
}

// printFullChatResponse reads a full (non-streaming) JSON response and prints the content.
func printFullChatResponse(body io.Reader) {
	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(body).Decode(&result); err != nil {
		fmt.Fprintf(os.Stderr, "  %s %s\n", "❌", ui.Red("Invalid response from gateway"))
		os.Exit(1)
	}
	if len(result.Choices) > 0 {
		fmt.Printf("  %s\n\n", result.Choices[0].Message.Content)
	}
}
