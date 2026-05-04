package main

import (
	"testing"
	"time"
)

func TestReadDurationHours_ZeroFallsBack(t *testing.T) {
	cases := []struct {
		name    string
		val     string
		wantHrs int
	}{
		{"zero", "0", 24},                    // n>0 fails → fallback
		{"negative", "-1", 24},               // n>0 fails → fallback
		{"out_of_range_720", "720", 24},       // n<720 fails (out of range) → fallback
		{"non_numeric", "abc", 24},            // strconv.Atoi error → fallback
		{"empty", "", 24},                     // empty/unset env → fallback
		{"valid_12", "12", 12},               // valid → 12h
		{"boundary_low_1", "1", 1},           // boundary low → 1h
		{"boundary_high_719", "719", 719},    // boundary high → 719h
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("EMDEX_CACHE_VACUUM_INTERVAL_HOURS", tc.val)
			got := readDurationHours("EMDEX_CACHE_VACUUM_INTERVAL_HOURS", 24)
			want := time.Duration(tc.wantHrs) * time.Hour
			if got != want {
				t.Fatalf("val=%q: got %v, want %v", tc.val, got, want)
			}
		})
	}
}
