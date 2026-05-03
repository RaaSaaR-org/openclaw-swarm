package email

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiskSenderWritesAllThreeArtifacts(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s, err := NewDiskSender(dir)
	if err != nil {
		t.Fatalf("NewDiskSender: %v", err)
	}
	if err := s.Send(context.Background(), Message{
		To:      "alice@kai.example",
		From:    "Kai <noreply@kai.example>",
		Subject: "Hi",
		HTML:    "<p>hello</p>",
		Text:    "hello",
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	exts := map[string]int{}
	for _, e := range entries {
		exts[filepath.Ext(e.Name())]++
	}
	for _, want := range []string{".eml", ".html", ".txt"} {
		if exts[want] != 1 {
			t.Errorf("expected exactly 1 %s file, got %d (entries=%v)", want, exts[want], entries)
		}
	}
	// .eml carries the envelope so callers can grep for "To: ..."
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".eml" {
			body, _ := os.ReadFile(filepath.Join(dir, e.Name()))
			if !strings.Contains(string(body), "To: alice@kai.example") {
				t.Errorf("eml body missing To header: %s", body)
			}
			if !strings.Contains(string(body), "Subject: Hi") {
				t.Errorf("eml body missing Subject header: %s", body)
			}
		}
	}
}

func TestDiskSenderSequenceAvoidsCollisions(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s, _ := NewDiskSender(dir)
	for i := 0; i < 10; i++ {
		if err := s.Send(context.Background(), Message{
			To: "x@y.z", From: "k@k", Subject: "s", HTML: "h", Text: "t",
		}); err != nil {
			t.Fatalf("Send #%d: %v", i, err)
		}
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 30 { // 10 sends × 3 files each
		t.Errorf("expected 30 files (10 sends × 3 artifacts), got %d", len(entries))
	}
}

func TestDiskSenderRejectsEmptyDir(t *testing.T) {
	t.Parallel()
	if _, err := NewDiskSender(""); err == nil {
		t.Error("expected error on empty Dir")
	}
}

func TestDiskSenderRejectsMissingAddress(t *testing.T) {
	t.Parallel()
	s, _ := NewDiskSender(t.TempDir())
	err := s.Send(context.Background(), Message{From: "k@k", Subject: "s"})
	if err == nil {
		t.Fatal("expected error when To is empty")
	}
}
