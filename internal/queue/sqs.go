package queue

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
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
	messages := make([]Message, 0, maxMessages)

	// SQS caps a single ReceiveMessage at 10 messages, so fetch in batches.
	// Long-poll only on the first call; once the queue has answered, drain
	// the rest with short polls.
	waitTime := int32(20)

	for len(messages) < maxMessages {
		batchSize := maxMessages - len(messages)
		if batchSize > 10 {
			batchSize = 10
		}

		res, err := c.sqs.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
			QueueUrl:            &queueURL,
			MaxNumberOfMessages: int32(batchSize),
			WaitTimeSeconds:     waitTime,
		})
		if err != nil {
			return nil, err
		}

		if len(res.Messages) == 0 {
			break
		}

		for _, msg := range res.Messages {
			messages = append(messages, Message{
				ID:            *msg.MessageId,
				Body:          *msg.Body,
				ReceiptHandle: *msg.ReceiptHandle,
			})
		}

		waitTime = 1
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

func (c *Client) ReplayWorkerPool(ctx context.Context, sourceQueueURL, targetQueueURL string, messages []Message, worker int) []error {

	jobs := make(chan Message, len(messages))
	results := make(chan error, len(messages))

	var wg sync.WaitGroup
	for i := 0; i < worker; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for m := range jobs {
				if err := c.replayAndDelete(ctx, sourceQueueURL, targetQueueURL, m); err != nil {
					results <- err
				}
			}
		}()
	}

	for _, m := range messages {
		jobs <- m
	}
	close(jobs)

	wg.Wait()
	close(results)

	var errs []error
	for err := range results {
		if err != nil {
			errs = append(errs, err)
		}
	}
	return errs
}

func (c *Client) replayAndDelete(ctx context.Context, sourceQueueURL, targetQueueURL string, m Message) error {
	if err := c.Replay(ctx, targetQueueURL, m.Body); err != nil {
		return err
	}

	return c.Delete(ctx, sourceQueueURL, m.ReceiptHandle)
}

func (c *Client) Delete(ctx context.Context, queueURL, receiptHandle string) error {
	_, err := c.sqs.DeleteMessage(ctx, &sqs.DeleteMessageInput{
		QueueUrl:      &queueURL,
		ReceiptHandle: &receiptHandle,
	})
	return err
}

// Release makes received messages visible again immediately (visibility
// timeout 0), so a read-only look at a queue does not hide its messages.
func (c *Client) Release(ctx context.Context, queueURL string, messages []Message) error {
	for i := 0; i < len(messages); i += 10 {
		batch := messages[i:min(i+10, len(messages))]

		entries := make([]types.ChangeMessageVisibilityBatchRequestEntry, len(batch))
		for j, m := range batch {
			entries[j] = types.ChangeMessageVisibilityBatchRequestEntry{
				Id:                aws.String(strconv.Itoa(j)),
				ReceiptHandle:     aws.String(m.ReceiptHandle),
				VisibilityTimeout: 0,
			}
		}

		res, err := c.sqs.ChangeMessageVisibilityBatch(ctx, &sqs.ChangeMessageVisibilityBatchInput{
			QueueUrl: &queueURL,
			Entries:  entries,
		})
		if err != nil {
			return err
		}
		if len(res.Failed) > 0 {
			return fmt.Errorf("failed to release %d message(s): %s", len(res.Failed), aws.ToString(res.Failed[0].Message))
		}
	}
	return nil
}

// Stats is a snapshot of a queue's attributes. Message counts are
// approximate, as reported by SQS.
type Stats struct {
	Visible                  int
	InFlight                 int
	Delayed                  int
	VisibilityTimeoutSeconds int
	RetentionSeconds         int
	RedrivePolicy            string
}

func (c *Client) Stats(ctx context.Context, queueURL string) (Stats, error) {
	res, err := c.sqs.GetQueueAttributes(ctx, &sqs.GetQueueAttributesInput{
		QueueUrl: &queueURL,
		AttributeNames: []types.QueueAttributeName{
			types.QueueAttributeNameApproximateNumberOfMessages,
			types.QueueAttributeNameApproximateNumberOfMessagesNotVisible,
			types.QueueAttributeNameApproximateNumberOfMessagesDelayed,
			types.QueueAttributeNameVisibilityTimeout,
			types.QueueAttributeNameMessageRetentionPeriod,
			types.QueueAttributeNameRedrivePolicy,
		},
	})
	if err != nil {
		return Stats{}, err
	}

	attr := func(name types.QueueAttributeName) int {
		n, _ := strconv.Atoi(res.Attributes[string(name)])
		return n
	}

	return Stats{
		Visible:                  attr(types.QueueAttributeNameApproximateNumberOfMessages),
		InFlight:                 attr(types.QueueAttributeNameApproximateNumberOfMessagesNotVisible),
		Delayed:                  attr(types.QueueAttributeNameApproximateNumberOfMessagesDelayed),
		VisibilityTimeoutSeconds: attr(types.QueueAttributeNameVisibilityTimeout),
		RetentionSeconds:         attr(types.QueueAttributeNameMessageRetentionPeriod),
		RedrivePolicy:            res.Attributes[string(types.QueueAttributeNameRedrivePolicy)],
	}, nil
}

// SourceQueues returns the URLs of queues whose redrive policy points at dlqURL.
func (c *Client) SourceQueues(ctx context.Context, dlqURL string) ([]string, error) {
	var urls []string

	p := sqs.NewListDeadLetterSourceQueuesPaginator(c.sqs, &sqs.ListDeadLetterSourceQueuesInput{
		QueueUrl: &dlqURL,
	})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		urls = append(urls, page.QueueUrls...)
	}

	return urls, nil
}

func MatchFilter(body, filter string) (bool, error) {
	if filter == "" {
		return true, nil
	}

	reg, err := regexp.Compile(filter)
	if err != nil {
		return false, err
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
