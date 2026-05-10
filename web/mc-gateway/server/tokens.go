package main

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"sync"

	"github.com/emai-ai/swarm/pkg/auth"
	"sigs.k8s.io/yaml"
)

// rawToken is one entry in the YAML file mc-gateway loads at startup.
type rawToken struct {
	Name       string `json:"name" yaml:"name"`
	Hash       string `json:"hash" yaml:"hash"`
	Role       string `json:"role" yaml:"role"`
	Slug       string `json:"slug,omitempty" yaml:"slug,omitempty"`
	CustomerID string `json:"customer_id,omitempty" yaml:"customer_id,omitempty"`
}

type rawTokensFile struct {
	Tokens []rawToken `json:"tokens" yaml:"tokens"`
}

// Role determines how the gateway scopes a request.
type Role string

const (
	RoleAdmin  Role = "admin"
	RoleTenant Role = "tenant"
)

// Token is one validated entry from the tokens file.
type Token struct {
	Name       string
	Role       Role
	Slug       string // tenants only
	CustomerID string // tenants only — the CUST-NNN id we scope to
	hash       string // argon2id PHC
}

// TokenStore holds the loaded tokens plus a SHA-256 fast-path cache so we
// only run argon2 once per never-before-seen bearer.
//
// argon2id verification is intentionally slow (≈30–100 ms per call). Without
// the cache, every request iterates every token at that cost — a trivial DoS
// vector at any production load. Entries land in the cache only on a
// successful verify, so an attacker hammering with random bearers cannot grow
// it; the cache is naturally bounded by len(tokens).
type TokenStore struct {
	tokens []Token
	mu     sync.RWMutex
	cache  map[[32]byte]int // sha256(bearer) → index into tokens
}

// LoadTokenStore reads the YAML file at path. Empty files and unknown roles
// are rejected at startup so a bad rollout fails loud.
func LoadTokenStore(path string) (*TokenStore, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parseTokenStore(raw)
}

func parseTokenStore(b []byte) (*TokenStore, error) {
	var f rawTokensFile
	if err := yaml.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("parse tokens yaml: %w", err)
	}
	if len(f.Tokens) == 0 {
		return nil, errors.New("tokens file is empty — define at least one token")
	}
	out := make([]Token, 0, len(f.Tokens))
	for i, t := range f.Tokens {
		if t.Name == "" {
			return nil, fmt.Errorf("token[%d]: name is required", i)
		}
		if t.Hash == "" {
			return nil, fmt.Errorf("token %q: hash is required", t.Name)
		}
		role := Role(t.Role)
		switch role {
		case RoleAdmin:
			// admin entries ignore slug/customer_id
		case RoleTenant:
			if t.Slug == "" || t.CustomerID == "" {
				return nil, fmt.Errorf("token %q: tenant role requires slug and customer_id", t.Name)
			}
		default:
			return nil, fmt.Errorf("token %q: unknown role %q (expected admin or tenant)", t.Name, t.Role)
		}
		out = append(out, Token{
			Name:       t.Name,
			Role:       role,
			Slug:       t.Slug,
			CustomerID: t.CustomerID,
			hash:       t.Hash,
		})
	}
	return &TokenStore{
		tokens: out,
		cache:  make(map[[32]byte]int),
	}, nil
}

// Len reports the number of loaded tokens. Used at startup logging.
func (s *TokenStore) Len() int {
	return len(s.tokens)
}

// Verify returns the matching Token, or nil if no entry matches `bearer`.
// Fast-path: SHA-256 of the bearer indexes a hashmap. Slow-path: argon2-
// verify against every token, then populate the cache on hit.
func (s *TokenStore) Verify(bearer string) *Token {
	if bearer == "" {
		return nil
	}
	digest := sha256.Sum256([]byte(bearer))

	s.mu.RLock()
	if idx, ok := s.cache[digest]; ok {
		s.mu.RUnlock()
		return &s.tokens[idx]
	}
	s.mu.RUnlock()

	// Slow path. Argon2 verification is the dominant cost; iterating once is fine.
	for i := range s.tokens {
		if auth.VerifyArgon2id(bearer, s.tokens[i].hash) {
			s.mu.Lock()
			// Re-check the cache under the write lock (another goroutine may
			// have populated it while we were verifying).
			if existing, ok := s.cache[digest]; ok {
				s.mu.Unlock()
				return &s.tokens[existing]
			}
			s.cache[digest] = i
			s.mu.Unlock()
			return &s.tokens[i]
		}
	}
	return nil
}

// cacheSize is for tests — exposes the current cache count.
func (s *TokenStore) cacheSize() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.cache)
}
