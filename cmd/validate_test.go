package cmd

import "testing"

func TestInspectOptionsValidate(t *testing.T) {
	tests := []struct {
		name    string
		opts    inspectOptions
		wantErr bool
	}{
		{
			name: "defaults are valid",
			opts: inspectOptions{since: "1h"},
		},
		{
			name:    "trace without log group",
			opts:    inspectOptions{since: "1h", trace: true},
			wantErr: true,
		},
		{
			name:    "invalid filter regex",
			opts:    inspectOptions{since: "1h", filter: "[invalid"},
			wantErr: true,
		},
		{
			name:    "invalid since",
			opts:    inspectOptions{since: "banana"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.opts.validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidateReplay(t *testing.T) {
	tests := []struct {
		name    string
		filter  string
		workers int
		wantErr bool
	}{
		{name: "valid", filter: "orderId", workers: 5},
		{name: "empty filter is valid", workers: 1},
		{name: "zero workers", workers: 0, wantErr: true},
		{name: "negative workers", workers: -1, wantErr: true},
		{name: "invalid filter regex", filter: "[invalid", workers: 1, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateReplay(tt.filter, tt.workers)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateReplay() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestResolveTimeRange(t *testing.T) {
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
			_, _, err := resolveTimeRange(tt.since, tt.start, tt.end)
			if (err != nil) != tt.wantErr {
				t.Errorf("resolveTimeRange() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
