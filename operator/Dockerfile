# Build the manager binary.
# Build context is the swarm repo root so the COPY commands can reach the
# sibling pkg/ modules that go.mod's replace directives point to (today
# pkg/quotas; future siblings land here too). Same pattern as
# web/customer-chat / web/customer-center / web/onboarding Dockerfiles.
# CI/release wire this up via -f operator/Dockerfile + context: .
FROM golang:1.25 AS builder
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev

WORKDIR /workspace
# Sibling pkg/ modules (replace targets) must exist before go mod download.
COPY pkg/ ./pkg/
WORKDIR /workspace/operator
COPY operator/go.mod go.mod
COPY operator/go.sum go.sum
RUN go mod download

# Copy the operator source.
COPY operator/api ./api
COPY operator/cmd ./cmd
COPY operator/internal ./internal
COPY operator/test ./test

# Build the manager binary. GOARCH unset → builds for the host platform
# (linux/arm64 on Apple Silicon, linux/amd64 elsewhere).
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build -ldflags "-X main.Version=${VERSION}" -a -o manager cmd/main.go

# Use distroless as minimal base image to package the manager binary.
# Refer to https://github.com/GoogleContainerTools/distroless for more details
FROM gcr.io/distroless/static:nonroot
WORKDIR /
COPY --from=builder /workspace/operator/manager .
USER 65532:65532

ENTRYPOINT ["/manager"]
