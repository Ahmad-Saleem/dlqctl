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
