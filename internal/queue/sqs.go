package queue

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"regexp"

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
	ID            string
	Body          string
	ReceiptHandle string
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

	if len(res.Messages) == 0 {
		fmt.Println("No messages found in the queue.")
		return nil, nil
	}

	messages := make([]Message, 0, len(res.Messages))

	for _, msg := range res.Messages {
		messages = append(messages, Message{
			ID:            *msg.MessageId,
			Body:          *msg.Body,
			ReceiptHandle: *msg.ReceiptHandle,
		})
	}

	return messages, nil
}

func (c *Client) Replay(ctx context.Context, targetQueueURL string, body string) error {
	_, err := c.sqs.SendMessage(ctx, &sqs.SendMessageInput{
		QueueUrl:    &targetQueueURL,
		MessageBody: &body,
	})
	return err
}

func (c *Client) Delete(ctx context.Context, queueURL, receiptHandle string) error {
	_, err := c.sqs.DeleteMessage(ctx, &sqs.DeleteMessageInput{
		QueueUrl:      &queueURL,
		ReceiptHandle: &receiptHandle,
	})
	return err
}

func MatchFilter(body, filter string) (bool, error) {
	var reg *regexp.Regexp
	var regErr error

	if filter != "" {
		reg, regErr = regexp.Compile(filter)
		if regErr != nil {
			return false, regErr
		}
	}

	return reg.MatchString(body), nil
}

func WriteJSON(w io.Writer, messages []Message) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")

	return encoder.Encode(messages)
}

func WriteCSV(w io.Writer, messages []Message) error {
	writer := csv.NewWriter(w)
	defer writer.Flush()
	if err := writer.Write([]string{"id", "body"}); err != nil {
		return err
	}

	for _, m := range messages {
		if err := writer.Write([]string{m.ID, m.Body}); err != nil {
			return err
		}
	}

	return writer.Error()
}
