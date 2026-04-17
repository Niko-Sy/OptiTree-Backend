package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	documentConversionQueueStream       = "stream:documents:conversion"
	documentConversionQueueGroup        = "doc-conversion-workers"
	documentConversionQueueMaxLen int64 = 20000
)

type documentConversionQueuePayload struct {
	DocumentID string `json:"documentId"`
	EnqueuedAt string `json:"enqueuedAt"`
	Source     string `json:"source,omitempty"`
}

type documentConversionQueueMessage struct {
	EntryID    string
	DocumentID string
	EnqueuedAt time.Time
}

func enqueueDocumentConversionTask(ctx context.Context, rdb *redis.Client, documentID, source string) error {
	if rdb == nil {
		return errors.New("redis is not configured")
	}

	docID := strings.TrimSpace(documentID)
	if docID == "" {
		return errors.New("document id is required")
	}

	payload := documentConversionQueuePayload{
		DocumentID: docID,
		EnqueuedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Source:     strings.TrimSpace(source),
	}
	rawPayload, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal conversion queue payload failed: %w", err)
	}

	if _, err := rdb.XAdd(ctx, &redis.XAddArgs{
		Stream: documentConversionQueueStream,
		MaxLen: documentConversionQueueMaxLen,
		Approx: true,
		Values: map[string]interface{}{
			"documentId": docID,
			"payload":    string(rawPayload),
		},
	}).Result(); err != nil {
		return fmt.Errorf("xadd conversion queue failed: %w", err)
	}

	_ = rdb.Expire(ctx, documentConversionQueueStream, documentConversionTaskTTL*2).Err()
	return nil
}

func parseDocumentConversionQueueMessage(msg redis.XMessage) (*documentConversionQueueMessage, error) {
	if strings.TrimSpace(msg.ID) == "" {
		return nil, errors.New("queue entry id is empty")
	}

	parseTime := func(value string) time.Time {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			return time.Now().UTC()
		}
		parsed, err := time.Parse(time.RFC3339Nano, trimmed)
		if err != nil {
			return time.Now().UTC()
		}
		return parsed.UTC()
	}

	if payloadRaw, ok := msg.Values["payload"]; ok {
		payloadText := strings.TrimSpace(fmt.Sprint(payloadRaw))
		if payloadText != "" {
			var payload documentConversionQueuePayload
			if err := json.Unmarshal([]byte(payloadText), &payload); err != nil {
				return nil, err
			}
			docID := strings.TrimSpace(payload.DocumentID)
			if docID == "" {
				return nil, errors.New("document id missing in payload")
			}
			return &documentConversionQueueMessage{
				EntryID:    msg.ID,
				DocumentID: docID,
				EnqueuedAt: parseTime(payload.EnqueuedAt),
			}, nil
		}
	}

	docID := strings.TrimSpace(fmt.Sprint(msg.Values["documentId"]))
	if docID == "" {
		return nil, errors.New("document id missing")
	}

	enqueuedAt := time.Now().UTC()
	if raw, ok := msg.Values["enqueuedAt"]; ok {
		switch value := raw.(type) {
		case string:
			enqueuedAt = parseTime(value)
		case int64:
			enqueuedAt = time.Unix(value, 0).UTC()
		case int:
			enqueuedAt = time.Unix(int64(value), 0).UTC()
		default:
			parsed, err := strconv.ParseInt(strings.TrimSpace(fmt.Sprint(raw)), 10, 64)
			if err == nil {
				enqueuedAt = time.Unix(parsed, 0).UTC()
			}
		}
	}

	return &documentConversionQueueMessage{
		EntryID:    msg.ID,
		DocumentID: docID,
		EnqueuedAt: enqueuedAt,
	}, nil
}
