# Build context is the swarm repo root (so the COPY commands can reach the
# sibling pkg/ module that go.mod's replace directive points to).
# CI/release wire this up via -f web/mc-gateway/Dockerfile + context: .

FROM golang:1.25-alpine AS go-build
WORKDIR /src
# Sibling pkg/ module (replace target) must exist before go mod download runs.
COPY pkg/ ./pkg/
WORKDIR /src/web/mc-gateway/server
COPY web/mc-gateway/server/go.mod web/mc-gateway/server/go.sum ./
RUN go mod download
COPY web/mc-gateway/server/*.go ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /mc-gateway .

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=go-build /mc-gateway /mc-gateway
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/mc-gateway"]
