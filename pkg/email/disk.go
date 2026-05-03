package email

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
)

// DiskSender writes rendered emails to a directory instead of dispatching
// them. Used in local development (EMAIL_PROVIDER=disk) so engineers can
// inspect the rendered HTML without provisioning a real provider account, and
// in CI so tests don't have to mock the network.
type DiskSender struct {
	Dir string
	seq atomic.Uint64
}

// NewDiskSender returns a DiskSender that writes under dir. The directory is
// created if missing; if it can't be created the constructor returns an error
// so callers fail at startup rather than at first send.
func NewDiskSender(dir string) (*DiskSender, error) {
	if dir == "" {
		return nil, fmt.Errorf("%w: empty Dir", ErrInvalidMessage)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create %s: %w", dir, err)
	}
	return &DiskSender{Dir: dir}, nil
}

// Send writes one .eml-ish file per call. The filename is timestamped +
// sequenced so concurrent sends don't collide and the directory listing is
// chronological. Three files per call: <stem>.html, <stem>.txt, <stem>.json
// (the last carries headers + envelope so callers can grep for "To: alice").
func (d *DiskSender) Send(_ context.Context, m Message) error {
	if m.To == "" || m.From == "" {
		return fmt.Errorf("%w: From and To are required", ErrInvalidMessage)
	}
	n := d.seq.Add(1)
	stem := fmt.Sprintf("%s-%04d-%s",
		time.Now().UTC().Format("20060102T150405"),
		n,
		safeAddr(m.To),
	)

	envelope := fmt.Sprintf(
		"From: %s\nTo: %s\nReply-To: %s\nSubject: %s\n\n--- HTML ---\n",
		m.From, m.To, m.ReplyTo, m.Subject,
	)

	for _, f := range []struct {
		ext     string
		content string
	}{
		{".eml", envelope + m.HTML + "\n\n--- TEXT ---\n" + m.Text + "\n"},
		{".html", m.HTML},
		{".txt", m.Text},
	} {
		if err := os.WriteFile(filepath.Join(d.Dir, stem+f.ext), []byte(f.content), 0o644); err != nil {
			return fmt.Errorf("write %s: %w", stem+f.ext, err)
		}
	}
	return nil
}

// safeAddr keeps the filename portable across filesystems by replacing the @
// and any other shell-noisy characters with `_`.
func safeAddr(addr string) string {
	repl := strings.NewReplacer("@", "_at_", "+", "_", "/", "_", " ", "_")
	return repl.Replace(addr)
}
