package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeUpstream records what the gateway forwarded so we can assert the proxy
// rewrote URLs / headers / bodies correctly.
type fakeUpstream struct {
	*httptest.Server
	mu       sync.Mutex
	requests []recordedReq
	respond  func(r recordedReq) (status int, body []byte, headers http.Header)
}

type recordedReq struct {
	method string
	path   string
	query  url.Values
	auth   string
	tenant string
	body   []byte
}

func newUpstream() *fakeUpstream {
	u := &fakeUpstream{}
	u.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		rec := recordedReq{
			method: r.Method,
			path:   r.URL.Path,
			query:  r.URL.Query(),
			auth:   r.Header.Get("Authorization"),
			tenant: r.Header.Get("X-Mc-Gateway-Tenant"),
			body:   body,
		}
		u.mu.Lock()
		u.requests = append(u.requests, rec)
		respond := u.respond
		u.mu.Unlock()

		status := http.StatusOK
		var respBody []byte
		var hdr http.Header
		if respond != nil {
			status, respBody, hdr = respond(rec)
		}
		for k, vs := range hdr {
			for _, v := range vs {
				w.Header().Add(k, v)
			}
		}
		if w.Header().Get("Content-Type") == "" {
			w.Header().Set("Content-Type", "application/json")
		}
		w.WriteHeader(status)
		_, _ = w.Write(respBody)
	}))
	return u
}

func (u *fakeUpstream) lastRequest() recordedReq {
	u.mu.Lock()
	defer u.mu.Unlock()
	if len(u.requests) == 0 {
		return recordedReq{}
	}
	return u.requests[len(u.requests)-1]
}

// fixture builds a gateway server with one admin token "ADMIN" and one tenant
// token "TENANT" scoped to slug=acme, customer_id=CUST-001.
func fixture(t *testing.T) (*server, *fakeUpstream) {
	t.Helper()
	yaml := fmt.Sprintf(`tokens:
  - name: kira-admin
    hash: "%s"
    role: admin
  - name: kai-acme
    hash: "%s"
    role: tenant
    slug: acme
    customer_id: CUST-001
`, mustHash(t, "ADMIN"), mustHash(t, "TENANT"))
	tokens, err := parseTokenStore([]byte(yaml))
	if err != nil {
		t.Fatal(err)
	}

	upstream := newUpstream()
	t.Cleanup(upstream.Close)
	upstreamURL, _ := url.Parse(upstream.URL)

	s := &server{
		tokens:        tokens,
		upstream:      upstreamURL,
		upstreamToken: "INTERNAL",
		client:        &http.Client{Timeout: 5 * time.Second},
	}
	return s, upstream
}

// roundTrip drives s.handleRequest with the given inputs and returns the
// recorder so tests can assert on status/body/headers.
func roundTrip(s *server, method, path, bearer string, body any) *httptest.ResponseRecorder {
	var bodyR io.Reader
	if body != nil {
		buf, _ := json.Marshal(body)
		bodyR = bytes.NewReader(buf)
	}
	req := httptest.NewRequest(method, path, bodyR)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rr := httptest.NewRecorder()
	s.handleRequest(rr, req)
	return rr
}

// ─────────────────── auth ───────────────────

func TestAuth_NoBearer_401(t *testing.T) {
	t.Parallel()
	s, _ := fixture(t)
	rr := roundTrip(s, "GET", "/v1/config", "", nil)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
	if got := rr.Header().Get("Content-Type"); got != "application/problem+json" {
		t.Fatalf("expected problem+json, got %q", got)
	}
}

func TestAuth_BadBearer_401(t *testing.T) {
	t.Parallel()
	s, _ := fixture(t)
	rr := roundTrip(s, "GET", "/v1/config", "wrong", nil)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}

// ─────────────────── public passthrough ───────────────────

func TestPublic_Passthrough_NoBearer(t *testing.T) {
	t.Parallel()
	s, up := fixture(t)
	up.respond = func(r recordedReq) (int, []byte, http.Header) {
		return http.StatusOK, []byte(`{"openapi":"3.1.0"}`), nil
	}
	rr := roundTrip(s, "GET", "/v1/openapi.json", "", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", rr.Code, rr.Body.String())
	}
	got := up.lastRequest()
	// Internal bearer must be set on the upstream call.
	if got.auth != "Bearer INTERNAL" {
		t.Fatalf("expected upstream Authorization=Bearer INTERNAL, got %q", got.auth)
	}
}

// ─────────────────── admin role pass-through ───────────────────

func TestAdmin_PassThrough_PostAdminOnly(t *testing.T) {
	t.Parallel()
	s, up := fixture(t)
	up.respond = func(r recordedReq) (int, []byte, http.Header) {
		return http.StatusOK, []byte(`{"customers":1}`), nil
	}
	rr := roundTrip(s, "POST", "/v1/index", "ADMIN", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	got := up.lastRequest()
	if got.tenant != "kira-admin" {
		t.Fatalf("expected X-Mc-Gateway-Tenant=kira-admin, got %q", got.tenant)
	}
}

// ─────────────────── tenant role: list endpoints ───────────────────

func TestTenant_List_InjectsCustomerFilter(t *testing.T) {
	t.Parallel()
	s, up := fixture(t)
	up.respond = func(r recordedReq) (int, []byte, http.Header) {
		return http.StatusOK, []byte(`[]`), nil
	}
	rr := roundTrip(s, "GET", "/v1/tasks", "TENANT", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	got := up.lastRequest()
	if got.query.Get("customer") != "CUST-001" {
		t.Fatalf("expected customer=CUST-001 forced into upstream query, got %q", got.query.Get("customer"))
	}
}

func TestTenant_List_RejectsCrossTenantFilter(t *testing.T) {
	t.Parallel()
	s, _ := fixture(t)
	rr := roundTrip(s, "GET", "/v1/tasks?customer=CUST-002", "TENANT", nil)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for cross-tenant filter, got %d (body=%s)", rr.Code, rr.Body.String())
	}
}

func TestTenant_List_AllowsOwnCustomerFilter(t *testing.T) {
	t.Parallel()
	s, up := fixture(t)
	up.respond = func(r recordedReq) (int, []byte, http.Header) {
		return http.StatusOK, []byte(`[]`), nil
	}
	rr := roundTrip(s, "GET", "/v1/tasks?customer=CUST-001", "TENANT", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	got := up.lastRequest()
	if got.query.Get("customer") != "CUST-001" {
		t.Fatalf("expected customer=CUST-001 in upstream query, got %q", got.query.Get("customer"))
	}
}

// ─────────────────── tenant role: create task ───────────────────

func TestTenant_CreateTask_RequiresOwnCustomerID(t *testing.T) {
	t.Parallel()
	s, _ := fixture(t)
	// Missing customer field
	rr := roundTrip(s, "POST", "/v1/tasks", "TENANT", map[string]any{
		"title": "Smoke test",
	})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 missing customer, got %d", rr.Code)
	}
	// Wrong customer field
	rr = roundTrip(s, "POST", "/v1/tasks", "TENANT", map[string]any{
		"title":    "Smoke test",
		"customer": "CUST-002",
	})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 wrong customer, got %d", rr.Code)
	}
}

func TestTenant_CreateTask_ForwardsWithOwnCustomerID(t *testing.T) {
	t.Parallel()
	s, up := fixture(t)
	up.respond = func(r recordedReq) (int, []byte, http.Header) {
		return http.StatusCreated, []byte(`{"id":"TASK-001","name":"Smoke","path":"/repo/customers/CUST-001-acme/tasks/todo/TASK-001-smoke.md"}`), nil
	}
	rr := roundTrip(s, "POST", "/v1/tasks", "TENANT", map[string]any{
		"title":    "Smoke test",
		"customer": "CUST-001",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d (body=%s)", rr.Code, rr.Body.String())
	}
	got := up.lastRequest()
	if got.method != "POST" || got.path != "/v1/tasks" {
		t.Fatalf("upstream request shape wrong: %+v", got)
	}
	// Body forwarded intact.
	var bodyMap map[string]any
	_ = json.Unmarshal(got.body, &bodyMap)
	if bodyMap["customer"] != "CUST-001" {
		t.Fatalf("body.customer not forwarded: %v", bodyMap)
	}
}

// ─────────────────── tenant role: move task ownership pre-check ───────────────────

func TestTenant_MoveTask_AllowsOwnTask(t *testing.T) {
	t.Parallel()
	s, up := fixture(t)
	// Gateway first GETs the entity to pre-check ownership, then POSTs the
	// move. Mock both paths.
	up.respond = func(r recordedReq) (int, []byte, http.Header) {
		switch {
		case r.method == "GET" && r.path == "/v1/entities/task/TASK-001":
			return http.StatusOK, []byte(`{"kind":"task","id":"TASK-001","frontmatter":{"customers":["[[CUST-001]]"]}}`), nil
		case r.method == "POST" && r.path == "/v1/tasks/TASK-001/move":
			return http.StatusOK, []byte(`{"id":"TASK-001","old_status":"backlog","new_status":"done","path":"/repo/customers/CUST-001-acme/tasks/done/TASK-001-smoke.md"}`), nil
		}
		return http.StatusInternalServerError, []byte(`{"detail":"unexpected upstream call: ` + r.method + ` ` + r.path + `"}`), nil
	}
	rr := roundTrip(s, "POST", "/v1/tasks/TASK-001/move", "TENANT", map[string]any{
		"status": "done",
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", rr.Code, rr.Body.String())
	}
}

func TestTenant_MoveTask_RejectsCrossTenantTask(t *testing.T) {
	t.Parallel()
	s, up := fixture(t)
	up.respond = func(r recordedReq) (int, []byte, http.Header) {
		// Pre-check returns the task owned by a different tenant.
		if r.method == "GET" && r.path == "/v1/entities/task/TASK-001" {
			return http.StatusOK, []byte(`{"kind":"task","id":"TASK-001","frontmatter":{"customers":["[[CUST-002]]"]}}`), nil
		}
		// If gateway is buggy and still forwards the move, mc-api would
		// happily process it — we return success so the test can assert
		// the gateway never reaches this code path.
		return http.StatusOK, []byte(`{"id":"TASK-001","old_status":"backlog","new_status":"done","path":"/repo/customers/CUST-002-other/tasks/done/TASK-001-smoke.md"}`), nil
	}
	rr := roundTrip(s, "POST", "/v1/tasks/TASK-001/move", "TENANT", map[string]any{
		"status": "done",
	})
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for cross-tenant move, got %d", rr.Code)
	}
	// Critical: the POST /move must NEVER have been forwarded upstream. mc
	// mutates the on-disk file when it processes a move; post-verifying the
	// response would be too late. This test pins the pre-check behavior:
	// gateway sees only the GET pre-check, not the POST.
	up.mu.Lock()
	defer up.mu.Unlock()
	for _, req := range up.requests {
		if req.method == "POST" && req.path == "/v1/tasks/TASK-001/move" {
			t.Fatalf("gateway forwarded the move to upstream — mutation would have landed before the 404. Upstream saw %d requests: %+v",
				len(up.requests), up.requests)
		}
	}
}

// TestTenant_MoveTask_PreCheckFailureDoesNotForward pins that any pre-check
// failure (upstream 404 on the GET, upstream 5xx, network error) refuses the
// move WITHOUT forwarding the POST. This is the close-fail property: a
// flaky upstream during the pre-check must not silently allow the write.
func TestTenant_MoveTask_PreCheckFailureDoesNotForward(t *testing.T) {
	t.Parallel()
	s, up := fixture(t)
	up.respond = func(r recordedReq) (int, []byte, http.Header) {
		if r.method == "GET" && r.path == "/v1/entities/task/TASK-001" {
			// Simulate upstream 404 (task doesn't exist or transient error).
			return http.StatusNotFound, []byte(`{"detail":"no such entity"}`), nil
		}
		return http.StatusOK, []byte(`{"path":"/repo/customers/CUST-001-acme/tasks/done/TASK-001-smoke.md"}`), nil
	}
	rr := roundTrip(s, "POST", "/v1/tasks/TASK-001/move", "TENANT", map[string]any{
		"status": "done",
	})
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404 when pre-check fails, got %d", rr.Code)
	}
	up.mu.Lock()
	defer up.mu.Unlock()
	for _, req := range up.requests {
		if req.method == "POST" {
			t.Fatalf("gateway forwarded the move despite pre-check failure — upstream saw POST %s", req.path)
		}
	}
}

// ─────────────────── tenant role: single GET post-verify ───────────────────

func TestTenant_GetEntity_AllowsOwn(t *testing.T) {
	t.Parallel()
	s, up := fixture(t)
	up.respond = func(r recordedReq) (int, []byte, http.Header) {
		return http.StatusOK, []byte(`{"kind":"task","id":"TASK-001","frontmatter":{"customers":["[[CUST-001]]"]}}`), nil
	}
	rr := roundTrip(s, "GET", "/v1/entities/task/TASK-001", "TENANT", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", rr.Code, rr.Body.String())
	}
}

func TestTenant_GetEntity_RejectsCrossTenant(t *testing.T) {
	t.Parallel()
	s, up := fixture(t)
	up.respond = func(r recordedReq) (int, []byte, http.Header) {
		return http.StatusOK, []byte(`{"kind":"task","id":"TASK-001","frontmatter":{"customers":["[[CUST-002]]"]}}`), nil
	}
	rr := roundTrip(s, "GET", "/v1/entities/task/TASK-001", "TENANT", nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for cross-tenant entity, got %d", rr.Code)
	}
}

// ─────────────────── tenant role: own-customer GET ───────────────────

func TestTenant_GetOwnCustomer(t *testing.T) {
	t.Parallel()
	s, up := fixture(t)
	up.respond = func(r recordedReq) (int, []byte, http.Header) {
		return http.StatusOK, []byte(`{"kind":"customer","id":"CUST-001"}`), nil
	}
	rr := roundTrip(s, "GET", "/v1/entities/customer/CUST-001", "TENANT", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
}

func TestTenant_GetOtherCustomer_404(t *testing.T) {
	t.Parallel()
	s, _ := fixture(t)
	rr := roundTrip(s, "GET", "/v1/entities/customer/CUST-002", "TENANT", nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for other tenant's customer, got %d", rr.Code)
	}
}

// ─────────────────── tenant role: admin-only endpoints ───────────────────

func TestTenant_CreateCustomer_Forbidden(t *testing.T) {
	t.Parallel()
	s, _ := fixture(t)
	rr := roundTrip(s, "POST", "/v1/customers", "TENANT", map[string]any{"name": "Acme"})
	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rr.Code)
	}
}

func TestTenant_Index_Forbidden(t *testing.T) {
	t.Parallel()
	s, _ := fixture(t)
	rr := roundTrip(s, "POST", "/v1/index", "TENANT", nil)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rr.Code)
	}
}

func TestTenant_Validate_Forbidden(t *testing.T) {
	t.Parallel()
	s, _ := fixture(t)
	rr := roundTrip(s, "POST", "/v1/validate", "TENANT", nil)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rr.Code)
	}
}

// ─────────────────── unknown route ───────────────────

func TestUnknownRoute_404(t *testing.T) {
	t.Parallel()
	s, _ := fixture(t)
	rr := roundTrip(s, "GET", "/v2/something", "ADMIN", nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rr.Code)
	}
}

// ─────────────────── upstream Authorization rewrite ───────────────────

func TestForward_StripsClientBearerAndSetsInternal(t *testing.T) {
	t.Parallel()
	s, up := fixture(t)
	up.respond = func(r recordedReq) (int, []byte, http.Header) {
		return http.StatusOK, []byte(`{"mode":"standalone"}`), nil
	}
	_ = roundTrip(s, "GET", "/v1/config", "ADMIN", nil)
	got := up.lastRequest()
	if got.auth != "Bearer INTERNAL" {
		t.Fatalf("expected upstream Authorization=Bearer INTERNAL, got %q", got.auth)
	}
	if !strings.Contains(got.tenant, "kira-admin") {
		t.Fatalf("expected X-Mc-Gateway-Tenant set, got %q", got.tenant)
	}
}
