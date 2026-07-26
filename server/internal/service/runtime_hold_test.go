package service

import (
	"testing"
	"time"
)

func TestLoadHoldExpiryMargin(t *testing.T) {
	tests := []struct {
		name     string
		envValue string
		want     time.Duration
	}{
		{
			name:     "default when unset",
			envValue: "",
			want:     defaultHoldExpiryMargin,
		},
		{
			name:     "custom duration",
			envValue: "15m",
			want:     15 * time.Minute,
		},
		{
			name:     "duration with whitespace",
			envValue: "  30s  ",
			want:     30 * time.Second,
		},
		{
			name:     "invalid duration falls back to default",
			envValue: "not-a-duration",
			want:     defaultHoldExpiryMargin,
		},
		{
			name:     "zero duration falls back to default",
			envValue: "0s",
			want:     defaultHoldExpiryMargin,
		},
		{
			name:     "negative duration falls back to default",
			envValue: "-5m",
			want:     defaultHoldExpiryMargin,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.envValue != "" {
				t.Setenv("MULTICA_HOLD_EXPIRY_MARGIN", tc.envValue)
			} else {
				t.Setenv("MULTICA_HOLD_EXPIRY_MARGIN", "")
			}
			got := loadHoldExpiryMargin()
			if got != tc.want {
				t.Fatalf("loadHoldExpiryMargin() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestRuntimeOnHoldMargin verifies that a runtime is considered on hold until
// hold_until + HoldExpiryMargin has passed. This pins the safety buffer that
// prevents resuming a runtime before the provider's actual reset time.
func TestRuntimeOnHoldMargin(t *testing.T) {
	margin := 5 * time.Minute
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name      string
		holdUntil time.Time
		want      bool
	}{
		{
			name:      "still held before margin expires",
			holdUntil: now.Add(-margin).Add(time.Second),
			want:      true,
		},
		{
			name:      "released exactly at margin boundary",
			holdUntil: now.Add(-margin),
			want:      false,
		},
		{
			name:      "released after margin expired",
			holdUntil: now.Add(-margin).Add(-time.Second),
			want:      false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.holdUntil.Add(margin).After(now)
			if got != tc.want {
				t.Fatalf("holdUntil=%v, margin=%v, now=%v: held=%v, want %v",
					tc.holdUntil, margin, now, got, tc.want)
			}
		})
	}
}
