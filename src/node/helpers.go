package main

import (
	"os"
	"strconv"
)

// contextModel returns the model to use for contextual retrieval context generation.
// Priority: EMDEX_CONTEXT_MODEL → EMDEX_LLM_MODEL → gemini-3-flash-preview.
func contextModel() string {
	if m := os.Getenv("EMDEX_CONTEXT_MODEL"); m != "" {
		return m
	}
	if m := os.Getenv("EMDEX_LLM_MODEL"); m != "" {
		return m
	}
	return "gemini-3-flash-preview"
}

// parseIntEnv parses an environment variable as an integer, returning def if unset or invalid.
func parseIntEnv(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return def
}

// parseFloatEnv parses an environment variable as a float64, returning def if unset or invalid.
func parseFloatEnv(key string, def float64) float64 {
	if v := os.Getenv(key); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f > 0 {
			return f
		}
	}
	return def
}
