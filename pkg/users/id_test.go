package users

import (
	"strings"
	"sync"
	"testing"
	"time"
)

func TestNewIDFormat(t *testing.T) {
	t.Parallel()
	id, err := NewID()
	if err != nil {
		t.Fatalf("NewID: %v", err)
	}
	if !strings.HasPrefix(id, IDPrefix) {
		t.Fatalf("missing prefix: %q", id)
	}
	body := strings.TrimPrefix(id, IDPrefix)
	if len(body) != 26 {
		t.Fatalf("ulid body = %d chars, want 26 (id=%q)", len(body), id)
	}
	for _, c := range body {
		if !strings.ContainsRune(crockford, c) {
			t.Fatalf("non-crockford char %q in %q", c, id)
		}
	}
}

func TestNewIDIsTimePrefixed(t *testing.T) {
	t.Parallel()
	// Two IDs minted ms apart sort in time order.
	older, err := newIDAt(time.Unix(1_700_000_000, 0))
	if err != nil {
		t.Fatal(err)
	}
	newer, err := newIDAt(time.Unix(1_700_000_001, 0))
	if err != nil {
		t.Fatal(err)
	}
	if older >= newer {
		t.Errorf("expected lex ordering older < newer, got %q >= %q", older, newer)
	}
}

func TestNewIDUniquenessUnderConcurrency(t *testing.T) {
	t.Parallel()
	const n = 1000
	ids := make([]string, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id, err := NewID()
			if err != nil {
				t.Errorf("NewID: %v", err)
				return
			}
			ids[i] = id
		}(i)
	}
	wg.Wait()
	seen := make(map[string]bool, n)
	for _, id := range ids {
		if seen[id] {
			t.Fatalf("collision: %q", id)
		}
		seen[id] = true
	}
}
