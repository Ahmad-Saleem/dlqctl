package logs

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
)

type LogEvent struct {
	Timestamp time.Time
	Message   string
}

type Client struct {
	cwl *cloudwatchlogs.Client
}

func NewClient(ctx context.Context, region string) (*Client, error) {
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region))
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	return &Client{
		cwl: cloudwatchlogs.NewFromConfig(cfg),
	}, nil
}

func (c *Client) Search(ctx context.Context, logGroup, pattern string, start, end time.Time, max int) ([]LogEvent, error) {
	var events []LogEvent
	var nextToken *string

	startMs := start.UnixMilli()
	endMs := end.UnixMilli()

	for {
		input := &cloudwatchlogs.FilterLogEventsInput{
			LogGroupName:  &logGroup,
			FilterPattern: &pattern,
			StartTime:     &startMs,
			EndTime:       &endMs,
			NextToken:     nextToken,
		}

		out, err := c.cwl.FilterLogEvents(ctx, input)
		if err != nil {
			return nil, fmt.Errorf("FilterLogEvents failed: %w", err)
		}

		for _, e := range out.Events {
			events = append(events, LogEvent{
				Timestamp: time.UnixMilli(*e.Timestamp),
				Message:   *e.Message,
			})

			if len(events) >= max {
				return events, nil
			}
		}

		nextToken = out.NextToken
		if nextToken == nil {
			break
		}
	}

	return events, nil
}
