package timeparse

import (
	"testing"
	"time"
)

func TestParseSinceMinutes(t *testing.T) {
	start, end, err := ParseSince("30m")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	diff := end.Sub(start)
	if diff != 30*time.Minute {
		t.Errorf("expected 30m difference, got %v", diff)
	}
}

func TestParseSinceDays(t *testing.T) {
	start, end, err := ParseSince("2d")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	diff := end.Sub(start)
	if diff != 48*time.Hour {
		t.Errorf("expected 48h difference, got %v", diff)
	}
}

func TestParseSinceInvalid(t *testing.T) {
	_, _, err := ParseSince("banana")
	if err == nil {
		t.Error("expected error for invalid input, got nil")
	}
}

func TestResolve(t *testing.T) {
	tests := []struct {
		name       string
		since      string
		start, end string
		wantErr    bool
	}{
		{name: "since only", since: "30m"},
		{name: "explicit range", start: "2026-07-01T00:00:00Z", end: "2026-07-02T00:00:00Z"},
		{name: "start without end", since: "1h", start: "2026-07-01T00:00:00Z", wantErr: true},
		{name: "end before start", start: "2026-07-02T00:00:00Z", end: "2026-07-01T00:00:00Z", wantErr: true},
		{name: "invalid start format", start: "yesterday", end: "2026-07-01T00:00:00Z", wantErr: true},
		{name: "invalid since", since: "banana", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := Resolve(tt.since, tt.start, tt.end)
			if (err != nil) != tt.wantErr {
				t.Errorf("Resolve() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
