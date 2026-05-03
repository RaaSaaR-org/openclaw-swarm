package email

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestResendSendHappyPath(t *testing.T) {
	t.Parallel()
	var got struct {
		method string
		auth   string
		ct     string
		body   resendRequest
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.method = r.Method
		got.auth = r.Header.Get("Authorization")
		got.ct = r.Header.Get("Content-Type")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &got.body)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"em_123"}`))
	}))
	defer srv.Close()

	s := &ResendSender{APIKey: "sk_test", BaseURL: srv.URL}
	err := s.Send(context.Background(), Message{
		From:    "Kai <noreply@kai.example>",
		To:      "alice@kai.example",
		ReplyTo: "support@kai.example",
		Subject: "Hi",
		HTML:    "<p>hello</p>",
		Text:    "hello",
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if got.method != http.MethodPost {
		t.Errorf("method = %q, want POST", got.method)
	}
	if got.auth != "Bearer sk_test" {
		t.Errorf("auth header = %q, want Bearer sk_test", got.auth)
	}
	if got.ct != "application/json" {
		t.Errorf("content-type = %q, want application/json", got.ct)
	}
	if len(got.body.To) != 1 || got.body.To[0] != "alice@kai.example" {
		t.Errorf("To = %v, want [alice@kai.example]", got.body.To)
	}
	if got.body.From != "Kai <noreply@kai.example>" {
		t.Errorf("From = %q", got.body.From)
	}
	if got.body.Subject != "Hi" || got.body.HTML != "<p>hello</p>" || got.body.Text != "hello" {
		t.Errorf("body parts wrong: %+v", got.body)
	}
	if got.body.ReplyTo != "support@kai.example" {
		t.Errorf("ReplyTo = %q", got.body.ReplyTo)
	}
}

func TestResendSendNon2xxSurfacesStatusAndBody(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"message":"invalid from address"}`))
	}))
	defer srv.Close()

	s := &ResendSender{APIKey: "sk_test", BaseURL: srv.URL}
	err := s.Send(context.Background(), Message{From: "x", To: "y", Subject: "s"})
	if err == nil {
		t.Fatal("expected error on 422")
	}
	if !strings.Contains(err.Error(), "422") || !strings.Contains(err.Error(), "invalid from address") {
		t.Errorf("error must include status and body, got: %v", err)
	}
}

func TestResendSendRejectsEmptyRequiredFields(t *testing.T) {
	t.Parallel()
	s := &ResendSender{APIKey: "sk_test", BaseURL: "http://unused"}
	cases := []Message{
		{To: "y", Subject: "s"},                   // missing From
		{From: "x", Subject: "s"},                 // missing To
		{From: "x", To: "y"},                      // missing Subject
	}
	for i, m := range cases {
		err := s.Send(context.Background(), m)
		if !errors.Is(err, ErrInvalidMessage) {
			t.Errorf("case %d: expected ErrInvalidMessage, got %v", i, err)
		}
	}
}

func TestNewResendSenderRejectsEmptyKey(t *testing.T) {
	t.Parallel()
	if _, err := NewResendSender(""); err == nil {
		t.Error("expected error on empty key")
	}
}
