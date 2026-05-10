module github.com/emai-ai/swarm-onboarding/cmd/gdpr-purge

go 1.25.3

require (
	github.com/emai-ai/swarm/pkg/users v0.0.0
	github.com/emai-ai/swarm/pkg/userspg v0.0.0
	github.com/jackc/pgx/v5 v5.7.6
)

// Sibling pkg/ modules in this repo; resolve via local replace so the
// Docker build (with the swarm repo root as context) can find them
// without published versions. Same pattern as web/onboarding/server/go.mod.
replace github.com/emai-ai/swarm/pkg/users => ../../../../pkg/users

replace github.com/emai-ai/swarm/pkg/userspg => ../../../../pkg/userspg

require (
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	golang.org/x/crypto v0.37.0 // indirect
	golang.org/x/sync v0.13.0 // indirect
	golang.org/x/text v0.24.0 // indirect
)
