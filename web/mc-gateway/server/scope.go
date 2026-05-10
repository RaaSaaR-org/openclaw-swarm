package main

import (
	"net/http"
	"regexp"
	"strings"
)

// classification of a request by its (method, path) pair so the proxy layer
// knows what to do.
type routeClass int

const (
	// publicPassthrough — gateway forwards the request without auth.
	// Targets mc's no-auth endpoints (/healthz, /readyz, /v1/openapi.json,
	// /v1/docs).
	publicPassthrough routeClass = iota
	// authedAdminOnly — admin tokens forwarded; tenant tokens get 403.
	authedAdminOnly
	// authedTenantList — list endpoint with optional `customer` filter.
	// Tenants get the customer filter forced; admins pass through.
	authedTenantList
	// authedTenantCreateTask — POST /v1/tasks. Tenants must include their own
	// customer_id in the body; otherwise 400.
	authedTenantCreateTask
	// authedTenantMoveTask — POST /v1/tasks/{id}/move. Tenants forward; we
	// post-verify the response path matches their customer subtree.
	authedTenantMoveTask
	// authedTenantSingleGet — GET /v1/entities/{kind}/{id} (or .../raw).
	// Tenants forward; we post-verify the response's customer field.
	authedTenantSingleGet
	// authedTenantOwnCustomerGet — GET /v1/entities/customer/{id}. Tenants
	// can only read their own customer record (id == their CUST-NNN).
	authedTenantOwnCustomerGet
	// authedAny — any authenticated token can call this; no scoping.
	authedAny
	// unknown — gateway returns 404 (we do not blindly proxy paths we don't
	// recognise; that prevents accidental surface drift).
	unknown
)

// classify maps a (method, path) to a routeClass and any extracted path
// parameters. Implements the table in swarm/docs/api/gateway-design.md §"Tenant role".
func classify(method, path string) (routeClass, map[string]string) {
	// Public endpoints (no auth, pass through to mc).
	switch {
	case method == http.MethodGet && (path == "/healthz" || path == "/readyz" || path == "/v1/openapi.json" || path == "/v1/docs"):
		return publicPassthrough, nil
	}

	// Authenticated meta — any token can read.
	if method == http.MethodGet && (path == "/v1/config" || path == "/v1/status") {
		return authedAny, nil
	}

	// /v1/tasks — list (GET) and create (POST).
	if path == "/v1/tasks" {
		switch method {
		case http.MethodGet:
			return authedTenantList, nil
		case http.MethodPost:
			return authedTenantCreateTask, nil
		}
	}

	// /v1/tasks/{id}/move
	if m := taskMoveRe.FindStringSubmatch(path); m != nil && method == http.MethodPost {
		return authedTenantMoveTask, map[string]string{"id": m[1]}
	}

	// /v1/entities/{kind}
	if m := entitiesListRe.FindStringSubmatch(path); m != nil && method == http.MethodGet {
		return authedTenantList, map[string]string{"kind": m[1]}
	}

	// /v1/entities/{kind}/{id} (parsed) and /v1/entities/{kind}/{id}/raw.
	if m := entitiesGetRe.FindStringSubmatch(path); m != nil && method == http.MethodGet {
		kind := m[1]
		params := map[string]string{"kind": kind, "id": m[2]}
		if kind == "customer" {
			return authedTenantOwnCustomerGet, params
		}
		return authedTenantSingleGet, params
	}

	// Plural-form create endpoints. Tenants are not allowed to create
	// top-level customers/projects/contacts; admins are.
	if method == http.MethodPost {
		switch path {
		case "/v1/customers", "/v1/projects", "/v1/contacts",
			"/v1/meetings", "/v1/research", "/v1/sprints", "/v1/proposals":
			return authedAdminOnly, nil
		case "/v1/index", "/v1/validate":
			return authedAdminOnly, nil
		}
	}

	return unknown, nil
}

var (
	// /v1/tasks/{id}/move
	taskMoveRe = regexp.MustCompile(`^/v1/tasks/([A-Z]+-\d+)/move$`)
	// /v1/entities/{kind}    (kind matches singular or plural slugs the mc API accepts)
	entitiesListRe = regexp.MustCompile(`^/v1/entities/([a-z]+)$`)
	// /v1/entities/{kind}/{id}  and  /v1/entities/{kind}/{id}/raw
	entitiesGetRe = regexp.MustCompile(`^/v1/entities/([a-z]+)/([A-Z]+-\d+)(?:/raw)?$`)
)

// normaliseKind maps singular/plural variants to mc's singular form. Tenants
// reach the API through this gateway only, so we keep our regex permissive
// and let mc reject unknown kinds upstream.
func normaliseKind(s string) string {
	if strings.HasSuffix(s, "s") {
		return strings.TrimSuffix(s, "s")
	}
	return s
}
