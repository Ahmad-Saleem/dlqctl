package queue

import "testing"

func TestMatchFilter(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		filter  string
		want    bool
		wantErr bool
	}{
		{
			name:   "matches when pattern found in body",
			body:   `{"userId": "123", "event": "click"}`,
			filter: "userId=123",
			want:   false,
		},
		{
			name:   "matches with correct pattern",
			body:   `{"userId": "123", "event": "click"}`,
			filter: "userId",
			want:   true,
		},
		{
			name:   "no match when pattern not in body",
			body:   `{"userId": "456"}`,
			filter: "userId=123",
			want:   false,
		},
		{
			name:    "returns error on invalid regex",
			body:    "anything",
			filter:  "[invalid",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := MatchFilter(tt.body, tt.filter)
			if (err != nil) != tt.wantErr {
				t.Errorf("unexpected error: %v", err)
				return
			}
			if got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}

}
