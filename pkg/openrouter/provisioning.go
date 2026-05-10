package openrouter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
)

// MintKeyParams describes a sub-key to be created via the provisioning API.
// All fields are optional; OpenRouter generates a default label when Label
// is empty. Limit is the per-key dollar cap — nil/0 means no cap.
type MintKeyParams struct {
	// Name is OpenRouter's human-readable name for the key (free-form).
	Name string `json:"name,omitempty"`
	// Label is what shows in the OpenRouter dashboard. We use the
	// workspace slug here so an operator scanning the dashboard can map
	// keys → tenants without leaving the page.
	Label string `json:"label,omitempty"`
	// Limit is the dollar cap. Pointer so we can distinguish "no cap"
	// (nil → omitempty in JSON) from "$0 cap".
	Limit *float64 `json:"limit,omitempty"`
}

// MintedKey is the response from POST /api/v1/keys. The Key field is the
// raw `sk-or-v1-…` token that the workspace pod uses to talk to
// OpenRouter; it's only returned ONCE — at mint time. Hash is the public
// identifier used for subsequent GET/PATCH/DELETE.
type MintedKey struct {
	Key  string  `json:"-"`     // populated from the top-level `key` field
	Hash string  `json:"hash"`  // identifier for follow-up operations
	Name string  `json:"name"`
	Label string `json:"label"`
	Limit *float64 `json:"limit,omitempty"`
}

// mintKeyEnvelope is the wire shape — `key` at the top level, the rest under `data`.
type mintKeyEnvelope struct {
	Key  string    `json:"key"`
	Data MintedKey `json:"data"`
}

// MintKey creates a new OpenRouter sub-key under this client's account.
// The client's APIKey must be a *provisioning* key (see OpenRouter docs at
// https://openrouter.ai/docs/use-cases/provisioning-api-keys). Returns the
// fresh key plus its identifying hash; the raw key is only returned once
// — store it now or it's gone.
func (c *Client) MintKey(ctx context.Context, params MintKeyParams) (*MintedKey, error) {
	body, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("openrouter mint: encode params: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL()+"/api/v1/keys", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("openrouter mint: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("openrouter mint %d: %s", resp.StatusCode, string(respBody))
	}
	var env mintKeyEnvelope
	if err := json.Unmarshal(respBody, &env); err != nil {
		return nil, fmt.Errorf("openrouter mint: parse: %w", err)
	}
	if env.Key == "" {
		return nil, errors.New("openrouter mint: empty key in response")
	}
	out := env.Data
	out.Key = env.Key
	return &out, nil
}

// RevokeKey deletes a sub-key by its hash. Idempotent on the OpenRouter side
// — re-revoking an already-revoked key returns 404 which we surface as an
// error so the caller knows the operation didn't change anything.
func (c *Client) RevokeKey(ctx context.Context, hash string) error {
	if hash == "" {
		return errors.New("openrouter revoke: empty hash")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.baseURL()+"/api/v1/keys/"+hash, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	req.Header.Set("Accept", "application/json")
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return fmt.Errorf("openrouter revoke: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("openrouter revoke %d: %s", resp.StatusCode, string(body))
	}
	return nil
}
