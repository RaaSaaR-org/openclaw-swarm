module github.com/emai-ai/swarm-mc-gateway

go 1.25.3

require (
	github.com/emai-ai/swarm/pkg/auth v0.0.0
	sigs.k8s.io/yaml v1.6.0
)

// pkg/auth is a sibling module — local replace so the Docker build (with the
// repo root as context) finds it without a published version.
replace github.com/emai-ai/swarm/pkg/auth => ../../../pkg/auth

require golang.org/x/crypto v0.50.0 // indirect

require (
	go.yaml.in/yaml/v2 v2.4.3 // indirect
	go.yaml.in/yaml/v3 v3.0.4 // indirect
	golang.org/x/sys v0.43.0 // indirect
)
