package queue

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
)

type Client struct {
	sqs *sqs.Client
}

func NewClient(ctx context.Context, region string) (*Client, error) {
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region))

	if err != nil {
		return nil, err
	}

	return &Client{
		sqs: sqs.NewFromConfig(cfg),
	}, nil
}

type Message struct {
	ID   string
	Body string
}

func (c *Client) Inspect(ctx context.Context, queueURL string, maxMessages int) ([]Message, error) {
	res, err := c.sqs.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
		QueueUrl:            &queueURL,
		MaxNumberOfMessages: int32(maxMessages),
		WaitTimeSeconds:     int32(20),
	})

	if err != nil {
		return nil, err
	}

	messages := make([]Message, 0, len(res.Messages))

	for _, msg := range res.Messages {
		messages = append(messages, Message{
			ID:   *msg.MessageId,
			Body: *msg.Body,
		})
	}

	return messages, nil
}
