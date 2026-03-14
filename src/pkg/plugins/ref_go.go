package main

import (
	"encoding/json"
	"fmt"
	"os"
)

type Input struct {
	FilePath string `json:"filepath"`
	MimeType string `json:"mime_type"`
	Bytes    []byte `json:"bytes"`
}

type Result struct {
	Text     string                 `json:"text"`
	Metadata map[string]interface{} `json:"metadata"`
	Error    string                 `json:"error,omitempty"`
}

func main() {
	var in Input
	if err := json.NewDecoder(os.Stdin).Decode(&in); err != nil {
		fmt.Printf(`{"error": "%s"}`, err)
		os.Exit(1)
	}

	result := Result{
		Text: fmt.Sprintf("Go Plugin: Processed %s", in.FilePath),
		Metadata: map[string]interface{}{
			"plugin":        "reference-go",
			"mime_type":     in.MimeType,
			"bytes_received": len(in.Bytes),
		},
	}

	json.NewEncoder(os.Stdout).Encode(result)
}
