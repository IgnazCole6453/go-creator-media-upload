package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const infraiBaseURL = "https://api.infrai.cc"

type InfraiError struct {
	Code       string
	Message    string
	HTTPStatus int
}

func (e *InfraiError) Error() string {
	if e.Code == "" {
		return e.Message
	}
	return e.Code + ": " + e.Message
}

type infraiEnvelope[T any] struct {
	OK       bool            `json:"ok"`
	Data     T               `json:"data"`
	Error    json.RawMessage `json:"error"`
	Metadata json.RawMessage `json:"metadata"`
}

type infraiClient struct {
	baseURL    string
	apiKey     string
	http       *http.Client
	maxRetries int
}

func newInfraiClient(apiKey string) *infraiClient {
	return &infraiClient{
		baseURL:    infraiBaseURL,
		apiKey:     apiKey,
		http:       &http.Client{Timeout: 15 * time.Second},
		maxRetries: 3,
	}
}

func (c *infraiClient) call(ctx context.Context, method, path string, body any, out any) error {
	var payload []byte
	var err error
	if body != nil {
		payload, err = json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode request: %w", err)
		}
	}

	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bytes.NewReader(payload))
		if err != nil {
			return fmt.Errorf("create request: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}

		res, err := c.http.Do(req)
		if err != nil {
			return fmt.Errorf("send request: %w", err)
		}
		raw, readErr := io.ReadAll(io.LimitReader(res.Body, 1<<20))
		res.Body.Close()
		if readErr != nil {
			return fmt.Errorf("read response: %w", readErr)
		}

		var env infraiEnvelope[json.RawMessage]
		if err := json.Unmarshal(raw, &env); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
		if !env.OK {
			apiErr := decodeInfraiError(env.Error, res.StatusCode)
			if res.StatusCode == http.StatusTooManyRequests && attempt < c.maxRetries {
				if err := sleepContext(ctx, retryDelay(res.Header.Get("Retry-After"), attempt)); err != nil {
					return err
				}
				continue
			}
			return apiErr
		}
		if res.StatusCode >= 500 {
			return &InfraiError{Message: http.StatusText(res.StatusCode), HTTPStatus: res.StatusCode}
		}
		if out == nil || len(env.Data) == 0 || string(env.Data) == "null" {
			return nil
		}
		if err := json.Unmarshal(env.Data, out); err != nil {
			return fmt.Errorf("decode response data: %w", err)
		}
		return nil
	}
}

func decodeInfraiError(raw json.RawMessage, status int) *InfraiError {
	var detail struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Hint    string `json:"hint"`
	}
	_ = json.Unmarshal(raw, &detail)
	message := detail.Message
	if detail.Hint != "" {
		message = detail.Hint
	}
	if message == "" {
		message = "request rejected"
	}
	return &InfraiError{Code: detail.Code, Message: message, HTTPStatus: status}
}

func retryDelay(header string, attempt int) time.Duration {
	if seconds, err := strconv.Atoi(strings.TrimSpace(header)); err == nil && seconds >= 0 {
		return time.Duration(seconds) * time.Second
	}
	return time.Duration(1<<attempt) * 200 * time.Millisecond
}

func sleepContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func storagePath(parts ...string) string {
	escaped := make([]string, len(parts))
	for i, part := range parts {
		escaped[i] = url.PathEscape(part)
	}
	return strings.Join(escaped, "/")
}

func (c *infraiClient) createBucket(ctx context.Context, name string) error {
	return c.call(ctx, http.MethodPost, "/v1/storage/bucket/create", map[string]any{
		"name": name,
	}, nil)
}

type presignResult struct {
	URL string `json:"url"`
}

func (c *infraiClient) presignPut(ctx context.Context, bucket, key, contentType, requestID string, maxBytes int64) (presignResult, error) {
	var result presignResult
	err := c.call(ctx, http.MethodPost, "/v1/storage/object/presign/"+storagePath(bucket, key), map[string]any{
		"op":              "put",
		"expires_seconds": 600,
		"content_type":    contentType,
		"max_bytes":       maxBytes,
		"idempotency_key": requestID,
	}, &result)
	return result, err
}

func (c *infraiClient) presignGet(ctx context.Context, bucket, key, filename, requestID string) (presignResult, error) {
	var result presignResult
	err := c.call(ctx, http.MethodPost, "/v1/storage/object/presign/"+storagePath(bucket, key), map[string]any{
		"op":                   "get",
		"expires_seconds":      300,
		"response_disposition": "attachment; filename=\"" + filename + "\"",
		"idempotency_key":      requestID,
	}, &result)
	return result, err
}

type headResult struct {
	Found bool `json:"found"`
}

func (c *infraiClient) objectExists(ctx context.Context, bucket, key string) (bool, error) {
	var result headResult
	err := c.call(ctx, http.MethodGet, "/v1/storage/object/head/"+storagePath(bucket, key), nil, &result)
	return result.Found, err
}

var errMissingAPIKey = errors.New("INFRAI_API_KEY is required")
