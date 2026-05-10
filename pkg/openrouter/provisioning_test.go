package openrouter

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMintKey_HappyPath(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/api/v1/keys" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		var got MintKeyParams
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if got.Label != "kai-anna" {
			t.Errorf("label = %q, want kai-anna", got.Label)
		}
		if got.Limit == nil || *got.Limit != 5.0 {
			t.Errorf("limit = %v, want 5.0", got.Limit)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"key": "sk-or-v1-fresh",
			"data": {
				"hash": "h_abc",
				"name": "anna",
				"label": "kai-anna",
				"limit": 5
			}
		}`))
	}))
	defer srv.Close()

	c, _ := NewClient("sk-or-v1-provisioning")
	c.BaseURL = srv.URL
	limit := 5.0
	got, err := c.MintKey(context.Background(), MintKeyParams{Label: "kai-anna", Limit: &limit})
	if err != nil {
		t.Fatalf("MintKey: %v", err)
	}
	if got.Key != "sk-or-v1-fresh" {
		t.Errorf("Key = %q", got.Key)
	}
	if got.Hash != "h_abc" {
		t.Errorf("Hash = %q", got.Hash)
	}
	if got.Label != "kai-anna" {
		t.Errorf("Label = %q", got.Label)
	}
}

func TestMintKey_NoLimit(t *testing.T) {
	t.Parallel()
	var seen MintKeyParams
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &seen)
		_, _ = w.Write([]byte(`{"key":"sk-or-v1-x","data":{"hash":"h"}}`))
	}))
	defer srv.Close()
	c, _ := NewClient("provisioning")
	c.BaseURL = srv.URL
	if _, err := c.MintKey(context.Background(), MintKeyParams{Label: "no-cap"}); err != nil {
		t.Fatal(err)
	}
	if seen.Limit != nil {
		t.Errorf("Limit should be nil/omitted, got %v", *seen.Limit)
	}
}

func TestMintKey_Non2xx(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"bad provisioning key"}`))
	}))
	defer srv.Close()
	c, _ := NewClient("bad")
	c.BaseURL = srv.URL
	_, err := c.MintKey(context.Background(), MintKeyParams{Label: "x"})
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Errorf("expected 401 error, got %v", err)
	}
}

func TestMintKey_RejectsEmptyKeyResponse(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// `key` missing — server bug or rotation. Don't store an empty key.
		_, _ = w.Write([]byte(`{"data":{"hash":"h_only"}}`))
	}))
	defer srv.Close()
	c, _ := NewClient("p")
	c.BaseURL = srv.URL
	_, err := c.MintKey(context.Background(), MintKeyParams{Label: "x"})
	if err == nil {
		t.Fatal("expected error for missing key field")
	}
}

func TestRevokeKey_HappyPath(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Fatalf("expected DELETE, got %s", r.Method)
		}
		if r.URL.Path != "/api/v1/keys/h_abc" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"data":{"deleted":true}}`))
	}))
	defer srv.Close()
	c, _ := NewClient("p")
	c.BaseURL = srv.URL
	if err := c.RevokeKey(context.Background(), "h_abc"); err != nil {
		t.Errorf("RevokeKey: %v", err)
	}
}

func TestRevokeKey_RejectsEmptyHash(t *testing.T) {
	t.Parallel()
	c, _ := NewClient("p")
	if err := c.RevokeKey(context.Background(), ""); err == nil {
		t.Fatal("expected error for empty hash")
	}
}

func TestRevokeKey_404Surfaces(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"not found"}`))
	}))
	defer srv.Close()
	c, _ := NewClient("p")
	c.BaseURL = srv.URL
	err := c.RevokeKey(context.Background(), "h_gone")
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Errorf("expected 404 error, got %v", err)
	}
}
