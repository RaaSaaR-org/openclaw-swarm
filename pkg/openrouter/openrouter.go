// Package openrouter is a tiny OpenRouter REST client for the SaaS-direction
// usage-tracker (TASK-019 Phase 2). Two endpoints today:
//
//   - GET /api/v1/key — returns per-key usage in USD (with daily/weekly/
//     monthly breakdowns) and rate-limit shape.
//   - GET /api/v1/credits — returns account-wide totals.
//
// Per-workspace usage tracking — the actual TASK-019 goal — leans on
// OpenRouter's Provisioning Keys: the deployment overlay holds ONE
// provisioning key, mints one sub-key per workspace at provision time, and
// the cron polls each per-workspace sub-key via KeyInfo() for usage. See
// README for the architecture sketch. This package ships the read side
// (KeyInfo + Credits); the cron + provisioning-key minting land in
// follow-up Phase 3 work.
//
// We deliberately don't pull in OpenRouter's official SDK — the API
// surface is small and a hand-rolled net/http client keeps the dep graph
// tight, matches the pattern of pkg/email's ResendSender.
package openrouter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// DefaultBaseURL is OpenRouter's production REST root. Override per Client
// for tests (httptest server) or for a private proxy.
const DefaultBaseURL = "https://openrouter.ai"

// Client is the API surface. Zero value isn't useful — call NewClient.
type Client struct {
	APIKey  string
	Client  *http.Client // optional; defaults to a 10s-timeout client
	BaseURL string       // optional; defaults to DefaultBaseURL — overridable for tests
}

// NewClient constructs a Client from an API key. Empty keys are rejected at
// construction so callers fail at startup rather than at first request.
func NewClient(apiKey string) (*Client, error) {
	if apiKey == "" {
		return nil, errors.New("openrouter: empty API key")
	}
	return &Client{APIKey: apiKey}, nil
}

func (c *Client) httpClient() *http.Client {
	if c.Client != nil {
		return c.Client
	}
	return &http.Client{Timeout: 10 * time.Second}
}

func (c *Client) baseURL() string {
	if c.BaseURL != "" {
		return c.BaseURL
	}
	return DefaultBaseURL
}

// KeyInfo is the shape returned by GET /api/v1/key. Usage fields are in
// USD (OpenRouter's accounting unit). Limit is null for keys without a
// cap. Label is OpenRouter's redacted display string for the key.
type KeyInfo struct {
	Label             string  `json:"label"`
	IsManagementKey   bool    `json:"is_management_key"`
	IsProvisioningKey bool    `json:"is_provisioning_key"`
	IsFreeTier        bool    `json:"is_free_tier"`
	Limit             float64 `json:"limit"`           // 0 when null in JSON; LimitSet reports presence
	LimitRemaining    float64 `json:"limit_remaining"` // 0 when null
	Usage             float64 `json:"usage"`
	UsageDaily        float64 `json:"usage_daily"`
	UsageWeekly       float64 `json:"usage_weekly"`
	UsageMonthly      float64 `json:"usage_monthly"`
}

// Credits is the shape returned by GET /api/v1/credits — account-wide
// totals across all keys.
type Credits struct {
	TotalCredits float64 `json:"total_credits"`
	TotalUsage   float64 `json:"total_usage"`
}

type keyResponse struct {
	Data KeyInfo `json:"data"`
}

type creditsResponse struct {
	Data Credits `json:"data"`
}

// GetKey returns the current key's usage info. The response captures the
// authenticated key — the API key carried in `c.APIKey` is the one
// queried, not a target key passed in.
func (c *Client) GetKey(ctx context.Context) (*KeyInfo, error) {
	var out keyResponse
	if err := c.do(ctx, http.MethodGet, "/api/v1/key", &out); err != nil {
		return nil, err
	}
	return &out.Data, nil
}

// GetCredits returns the account-wide credit totals.
func (c *Client) GetCredits(ctx context.Context) (*Credits, error) {
	var out creditsResponse
	if err := c.do(ctx, http.MethodGet, "/api/v1/credits", &out); err != nil {
		return nil, err
	}
	return &out.Data, nil
}

func (c *Client) do(ctx context.Context, method, path string, into any) error {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL()+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return fmt.Errorf("openrouter: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("openrouter %d %s: %s", resp.StatusCode, path, string(body))
	}
	if err := json.Unmarshal(body, into); err != nil {
		return fmt.Errorf("openrouter parse %s: %w", path, err)
	}
	return nil
}
