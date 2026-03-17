package core

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

type LocalInstanceClient struct{}

func NewLocalInstanceClient() *LocalInstanceClient {
	return &LocalInstanceClient{}
}

func (c *LocalInstanceClient) Ask(ctx context.Context, socketPath string, req AskRequest) (AskResult, error) {
	var out AskResult
	socketPath = strings.TrimSpace(socketPath)
	if socketPath == "" {
		return out, fmt.Errorf("socket path is required")
	}

	payload, err := json.Marshal(req)
	if err != nil {
		return out, fmt.Errorf("marshal ask request: %w", err)
	}

	httpClient := &http.Client{
		Timeout: 5 * time.Minute,
		Transport: &http.Transport{
			DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
				return net.Dial("unix", socketPath)
			},
		},
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://unix/ask", bytes.NewReader(payload))
	if err != nil {
		return out, fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return out, fmt.Errorf("request /ask failed: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return out, fmt.Errorf("ask status=%d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var raw struct {
		Status    string `json:"status"`
		Content   string `json:"content"`
		LatencyMS int64  `json:"latency_ms"`
		ToolCount int    `json:"tool_count"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return out, fmt.Errorf("decode ask response: %w", err)
	}

	out.Content = raw.Content
	out.LatencyMS = raw.LatencyMS
	out.ToolCount = raw.ToolCount
	return out, nil
}

func (c *LocalInstanceClient) Send(ctx context.Context, socketPath string, req SendRequest) error {
	socketPath = strings.TrimSpace(socketPath)
	if socketPath == "" {
		return fmt.Errorf("socket path is required")
	}

	payload, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal send request: %w", err)
	}

	httpClient := &http.Client{
		Timeout: 2 * time.Minute,
		Transport: &http.Transport{
			DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
				return net.Dial("unix", socketPath)
			},
		},
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://unix/send", bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("request /send failed: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("send status=%d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}
