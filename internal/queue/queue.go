package queue

import "context"

type Queue interface {
	Inspect(ctx context.Context, queueURL string, max int) ([]Message, error)
	Replay(ctx context.Context, queueURL string, body string) error
	ReplayWorkerPool(ctx context.Context, from, to string, messages []Message, workers int) []error
	Delete(ctx context.Context, queueURL string, receiptHandle string) error
}
