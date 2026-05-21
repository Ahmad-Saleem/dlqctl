package extract

import "testing"

func TestFieldExplicit(t *testing.T) {
	body := `{"requestId": "abc-123", "userId": "u-456"}`
	val, err := Field(body, "requestId")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if val != "abc-123" {
		t.Errorf("expected abc-123, got %s", val)
	}
}

func TestFieldAutoDetect(t *testing.T) {
	body := `{"correlationId": "corr-789", "data": "something"}`
	val, err := Field(body, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if val != "corr-789" {
		t.Errorf("expected corr-789, got %s", val)
	}
}

func TestFieldMissing(t *testing.T) {
	body := `{"userId": "u-456", "data": "something"}`
	_, err := Field(body, "")
	if err == nil {
		t.Error("expected error when no trace field found, got nil")
	}
}

func TestFieldInvalidJSON(t *testing.T) {
	_, err := Field("not json", "requestId")
	if err == nil {
		t.Error("expected error for invalid JSON, got nil")
	}
}
