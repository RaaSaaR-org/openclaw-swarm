# Build context is the swarm repo root (so the COPY commands can reach the
# sibling pkg/ modules that go.mod's replace directives point to —
# pkg/auth, pkg/email, pkg/users since TASK-013 Phase 0).
# CI/release wire this up via -f web/onboarding/Dockerfile + context: .

FROM node:22-alpine AS web-build
WORKDIR /web
COPY web/onboarding/package.json web/onboarding/package-lock.json ./
RUN npm ci
COPY web/onboarding/tsconfig.json web/onboarding/vite.config.ts web/onboarding/index.html ./
COPY web/onboarding/src ./src
RUN npm run build

FROM golang:1.25-alpine AS go-build
WORKDIR /src
# Sibling pkg/ modules (replace targets) must exist before go mod download runs.
COPY pkg/ ./pkg/
WORKDIR /src/web/onboarding/server
COPY web/onboarding/server/go.mod web/onboarding/server/go.sum ./
RUN go mod download
COPY web/onboarding/server/*.go ./
COPY --from=web-build /web/dist ./web
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /onboarding .

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=go-build /onboarding /onboarding
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/onboarding"]
