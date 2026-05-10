package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
)

// handleRequest is the single dispatcher behind every non-health route.
// We classify the path, enforce auth + tenant scoping, then proxy upstream.
func (s *server) handleRequest(w http.ResponseWriter, r *http.Request) {
	cls, params := classify(r.Method, r.URL.Path)

	switch cls {
	case unknown:
		writeProblem(w, http.StatusNotFound, "not-found", "Not found",
			"no such endpoint")
		return

	case publicPassthrough:
		s.forward(w, r, nil)
		return
	}

	// All remaining classes require a bearer.
	bearer := extractBearer(r)
	if bearer == "" {
		writeProblem(w, http.StatusUnauthorized, "unauthenticated",
			"Authentication required", "missing bearer token")
		return
	}
	tok := s.tokens.Verify(bearer)
	if tok == nil {
		writeProblem(w, http.StatusUnauthorized, "unauthenticated",
			"Authentication required", "invalid bearer token")
		return
	}

	switch cls {
	case authedAny:
		s.forward(w, r, tok)
		return

	case authedAdminOnly:
		if tok.Role != RoleAdmin {
			writeProblem(w, http.StatusForbidden, "forbidden", "Forbidden",
				"this endpoint is admin-only")
			return
		}
		s.forward(w, r, tok)
		return

	case authedTenantList:
		if tok.Role == RoleAdmin {
			s.forward(w, r, tok)
			return
		}
		// Tenant: force ?customer=<their CUST-id>. If the client supplied a
		// different value, reject — we don't silently rewrite.
		q := r.URL.Query()
		if got := q.Get("customer"); got != "" && got != tok.CustomerID {
			writeProblem(w, http.StatusBadRequest, "bad-request",
				"Bad request",
				fmt.Sprintf("tenant token can only filter by customer=%s", tok.CustomerID))
			return
		}
		q.Set("customer", tok.CustomerID)
		r.URL.RawQuery = q.Encode()
		s.forward(w, r, tok)
		return

	case authedTenantCreateTask:
		if tok.Role == RoleAdmin {
			s.forward(w, r, tok)
			return
		}
		// Tenant: parse JSON body, require customer == tok.CustomerID. We do
		// not auto-inject — Kai's mc-client.sh wrapper sets it; if a request
		// arrives without it, the client is misconfigured and a 400 is the
		// right thing to surface.
		body, err := readJSONBody(r)
		if err != nil {
			writeProblem(w, http.StatusBadRequest, "bad-request", "Bad request",
				"invalid JSON body: "+err.Error())
			return
		}
		got, _ := body["customer"].(string)
		if got != tok.CustomerID {
			writeProblem(w, http.StatusBadRequest, "bad-request", "Bad request",
				fmt.Sprintf("body.customer must be %q for this tenant token (got %q)",
					tok.CustomerID, got))
			return
		}
		// Restore body for forwarding.
		buf, _ := json.Marshal(body)
		r.Body = io.NopCloser(bytes.NewReader(buf))
		r.ContentLength = int64(len(buf))
		s.forward(w, r, tok)
		return

	case authedTenantMoveTask:
		if tok.Role == RoleAdmin {
			s.forward(w, r, tok)
			return
		}
		// Tenant: forward, then post-verify the response's `path` includes
		// the tenant's customer dir. On mismatch, return 404 (avoids
		// existence leaks). The task ID is in the URL path; we don't have
		// the customer until the upstream tells us.
		s.forwardWithPostVerify(w, r, tok, params, verifyMovePath)
		return

	case authedTenantSingleGet:
		if tok.Role == RoleAdmin {
			s.forward(w, r, tok)
			return
		}
		// Tenant: forward, then check the response's frontmatter.customer
		// matches the tenant. /raw responses are markdown — we re-route those
		// through a dedicated check.
		s.forwardWithPostVerify(w, r, tok, params, verifyEntityCustomer)
		return

	case authedTenantOwnCustomerGet:
		if tok.Role == RoleAdmin {
			s.forward(w, r, tok)
			return
		}
		// Tenants may only GET their own customer record.
		if id := params["id"]; id != tok.CustomerID {
			writeProblem(w, http.StatusNotFound, "not-found", "Not found",
				"no such entity")
			return
		}
		s.forward(w, r, tok)
		return
	}
}

// forward proxies the request upstream with the gateway's internal token.
func (s *server) forward(w http.ResponseWriter, r *http.Request, tok *Token) {
	resp, err := s.callUpstream(r, tok)
	if err != nil {
		writeProblem(w, http.StatusBadGateway, "upstream", "Bad gateway",
			"upstream error: "+err.Error())
		return
	}
	defer resp.Body.Close()
	copyResponse(w, resp)
}

// forwardWithPostVerify proxies upstream, then runs `check` on the response.
// `check` returns nil on success, or a (status, code, detail) triple to send
// back to the client instead of the upstream body.
type postVerifyFn func(tok *Token, params map[string]string, status int, body []byte) (int, string, string)

func (s *server) forwardWithPostVerify(w http.ResponseWriter, r *http.Request, tok *Token, params map[string]string, check postVerifyFn) {
	resp, err := s.callUpstream(r, tok)
	if err != nil {
		writeProblem(w, http.StatusBadGateway, "upstream", "Bad gateway",
			"upstream error: "+err.Error())
		return
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		writeProblem(w, http.StatusBadGateway, "upstream", "Bad gateway",
			"read upstream body: "+err.Error())
		return
	}
	if rejStatus, rejCode, rejDetail := check(tok, params, resp.StatusCode, body); rejCode != "" {
		writeProblem(w, rejStatus, rejCode, "Forbidden", rejDetail)
		return
	}
	copyHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(body)
}

// verifyEntityCustomer post-checks GET /v1/entities/{kind}/{id} (and /raw).
// For JSON responses, parse `frontmatter.customer` (a wikilink like
// "[[CUST-001]]") and compare to the tenant's customer id. Markdown /raw
// responses go through the same check by string-searching the YAML head.
func verifyEntityCustomer(tok *Token, _ map[string]string, status int, body []byte) (int, string, string) {
	if status >= 300 {
		// Upstream already errored; pass it through.
		return 0, "", ""
	}
	if matchesCustomerID(body, tok.CustomerID) {
		return 0, "", ""
	}
	return http.StatusNotFound, "not-found", "no such entity"
}

// verifyMovePath post-checks POST /v1/tasks/{id}/move. Upstream returns
// {"path": ".../customers/CUST-NNN-<slug>/tasks/.../"}. Tenant access is
// allowed iff that path contains "/CUST-<NNN>-" matching their customer id.
func verifyMovePath(tok *Token, _ map[string]string, status int, body []byte) (int, string, string) {
	if status >= 300 {
		return 0, "", ""
	}
	var resp struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return http.StatusBadGateway, "upstream", "upstream returned non-JSON"
	}
	if strings.Contains(resp.Path, "/"+tok.CustomerID+"-") {
		return 0, "", ""
	}
	return http.StatusNotFound, "not-found", "no such entity"
}

// matchesCustomerID checks whether the body references the tenant's
// customer id. Works for both JSON entity responses (where customer
// appears in frontmatter as a wikilink) and raw-markdown responses
// (where the same wikilink appears in the YAML head).
func matchesCustomerID(body []byte, customerID string) bool {
	// JSON path: frontmatter.customer might be a string "[[CUST-NNN]]"
	// or an array of wikilinks under `customers`. Cheap path: substring.
	// CUST-NNN ids are unique enough across the JSON shape that a literal
	// match is safe — they only appear inside frontmatter or paths.
	return bytes.Contains(body, []byte(customerID))
}

// callUpstream rewrites the inbound request to target the upstream URL with
// the gateway's internal bearer.
func (s *server) callUpstream(r *http.Request, tok *Token) (*http.Response, error) {
	target := *s.upstream
	target.Path = r.URL.Path
	target.RawQuery = r.URL.RawQuery

	req, err := http.NewRequestWithContext(r.Context(), r.Method, target.String(), r.Body)
	if err != nil {
		return nil, err
	}
	// Copy headers, except Authorization (we replace) and Host (set by client).
	for k, vs := range r.Header {
		if strings.EqualFold(k, "Authorization") || strings.EqualFold(k, "Host") {
			continue
		}
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	req.Header.Set("Authorization", "Bearer "+s.upstreamToken)
	if tok != nil {
		req.Header.Set("X-Mc-Gateway-Tenant", tok.Name)
		log.Printf("mc-gateway: %s %s tenant=%s", r.Method, r.URL.Path, tok.Name)
	} else {
		log.Printf("mc-gateway: %s %s public", r.Method, r.URL.Path)
	}
	if r.ContentLength > 0 {
		req.ContentLength = r.ContentLength
	}
	return s.client.Do(req)
}

// readJSONBody decodes the request body as a generic JSON object, with the
// 64 KiB body cap mc itself enforces. The body is consumed; callers must
// re-set r.Body before forwarding upstream.
func readJSONBody(r *http.Request) (map[string]any, error) {
	r.Body = http.MaxBytesReader(nil, r.Body, 64*1024)
	defer r.Body.Close()
	out := map[string]any{}
	if err := json.NewDecoder(r.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}

func extractBearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if h == "" {
		return ""
	}
	const p = "Bearer "
	const lp = "bearer "
	switch {
	case strings.HasPrefix(h, p):
		return strings.TrimSpace(h[len(p):])
	case strings.HasPrefix(h, lp):
		return strings.TrimSpace(h[len(lp):])
	}
	return ""
}

func copyResponse(w http.ResponseWriter, resp *http.Response) {
	copyHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func copyHeaders(dst, src http.Header) {
	for k, vs := range src {
		// Hop-by-hop headers we don't pass through; they relate to the
		// gateway↔upstream connection, not the client↔gateway one.
		switch strings.ToLower(k) {
		case "connection", "proxy-connection", "keep-alive",
			"transfer-encoding", "te", "trailer", "upgrade":
			continue
		}
		for _, v := range vs {
			dst.Add(k, v)
		}
	}
}

func writeProblem(w http.ResponseWriter, status int, code, title, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"type":   "https://docs.mc.dev/errors/" + code,
		"title":  title,
		"status": status,
		"detail": detail,
	})
}
