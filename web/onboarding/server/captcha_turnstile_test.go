package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestTurnstileVerifyHappyPath(t *testing.T) {
	var gotForm url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST, got %s", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/x-www-form-urlencoded") {
			t.Fatalf("expected form-encoded body, got %q", ct)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		gotForm = r.PostForm
		json.NewEncoder(w).Encode(turnstileResponse{Success: true})
	}))
	defer srv.Close()

	c := newTurnstileCaptcha("secret-xyz")
	c.endpoint = srv.URL

	if err := c.Verify(context.Background(), "client-token-abc", "203.0.113.7"); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
	if got := gotForm.Get("secret"); got != "secret-xyz" {
		t.Errorf("secret: got %q", got)
	}
	if got := gotForm.Get("response"); got != "client-token-abc" {
		t.Errorf("response: got %q", got)
	}
	if got := gotForm.Get("remoteip"); got != "203.0.113.7" {
		t.Errorf("remoteip: got %q", got)
	}
}

func TestTurnstileVerifyOmitsRemoteIPWhenEmpty(t *testing.T) {
	var gotForm url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotForm = r.PostForm
		json.NewEncoder(w).Encode(turnstileResponse{Success: true})
	}))
	defer srv.Close()

	c := newTurnstileCaptcha("s")
	c.endpoint = srv.URL
	if err := c.Verify(context.Background(), "tok", ""); err != nil {
		t.Fatal(err)
	}
	if _, present := gotForm["remoteip"]; present {
		t.Errorf("remoteip should be omitted when empty, got %v", gotForm["remoteip"])
	}
}

func TestTurnstileVerifyRejectsEmptyToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatal("siteverify must not be called for empty tokens")
	}))
	defer srv.Close()

	c := newTurnstileCaptcha("s")
	c.endpoint = srv.URL
	if err := c.Verify(context.Background(), "   ", "1.1.1.1"); err == nil {
		t.Fatal("expected error for empty token")
	}
}

func TestTurnstileVerifyFailureCarriesErrorCodes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode(turnstileResponse{
			Success:    false,
			ErrorCodes: []string{"timeout-or-duplicate", "bad-request"},
		})
	}))
	defer srv.Close()

	c := newTurnstileCaptcha("s")
	c.endpoint = srv.URL
	err := c.Verify(context.Background(), "stale", "")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "timeout-or-duplicate") || !strings.Contains(err.Error(), "bad-request") {
		t.Errorf("error should include codes, got %q", err.Error())
	}
}

func TestTurnstileVerifyNon2xxSurfacesStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		io.WriteString(w, "boom")
	}))
	defer srv.Close()

	c := newTurnstileCaptcha("s")
	c.endpoint = srv.URL
	err := c.Verify(context.Background(), "tok", "")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error should include status, got %q", err.Error())
	}
}

func TestTurnstileVerifyMissingSecret(t *testing.T) {
	c := &turnstileCaptcha{} // no secret set
	if err := c.Verify(context.Background(), "tok", ""); err == nil {
		t.Fatal("expected error when secret unset")
	}
}
