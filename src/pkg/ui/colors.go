package ui

import (
	"fmt"
	"time"
)

// ANSI color codes
const (
	Reset  = "\033[0m"
	bold   = "\033[1m"
	red    = "\033[31m"
	green  = "\033[32m"
	yellow = "\033[33m"
	cyan   = "\033[36m"
	dim    = "\033[2m"
)

func Bold(s string) string   { return bold + s + Reset }
func Green(s string) string  { return green + s + Reset }
func Red(s string) string    { return red + s + Reset }
func Yellow(s string) string { return yellow + s + Reset }
func Cyan(s string) string   { return cyan + s + Reset }
func Dim(s string) string    { return dim + s + Reset }

// FormatTimeAgo parses an RFC3339 timestamp and returns a human-readable duration.
func FormatTimeAgo(ts string) string {
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return ts
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh%dm ago", int(d.Hours()), int(d.Minutes())%60)
	}
}
