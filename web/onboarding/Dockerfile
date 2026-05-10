# Build context is the swarm repo root (so the COPY commands can reach the
# sibling pkg/ modules that go.mod's replace directives point to —
# pkg/auth, pkg/email, pkg/users since TASK-013 Phase 0; pkg/userspg for
# the gdpr-purge cron added in TASK-021 Phase 2 — and the sibling
# web/shared/ branding contract).
# CI/release wire this up via -f web/onboarding/Dockerfile + context: .

FROM node:22-alpine AS web-build
WORKDIR /web
COPY web/shared ./shared
WORKDIR /web/onboarding
COPY web/onboarding/package.json web/onboarding/package-lock.json ./
RUN npm ci
COPY web/onboarding/tsconfig.json web/onboarding/vite.config.ts web/onboarding/index.html ./
COPY web/onboarding/src ./src
RUN npm run build

FROM golang:1.25-alpine AS go-build
WORKDIR /src
# Sibling pkg/ modules (replace targets) must exist before go mod download runs.
COPY pkg/ ./pkg/

# --- onboarding server (long-running web/API pod) ---
WORKDIR /src/web/onboarding/server
COPY web/onboarding/server/go.mod web/onboarding/server/go.sum ./
RUN go mod download
COPY web/onboarding/server/*.go ./
COPY --from=web-build /web/onboarding/dist ./web
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /onboarding .

# --- gdpr-purge cron (TASK-021 Phase 2) ---
# Lives in its own go module so the public swarm repo can opt in to the
# pgx dep here without forcing it into the long-running server binary.
# All three binaries ship in the final image; each CronJob entrypoint
# picks the relevant one.
WORKDIR /src/web/onboarding/cmd/gdpr-purge
COPY web/onboarding/cmd/gdpr-purge/go.mod web/onboarding/cmd/gdpr-purge/go.sum ./
RUN go mod download
COPY web/onboarding/cmd/gdpr-purge/main.go ./
COPY web/onboarding/cmd/gdpr-purge/internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /gdpr-purge .

# --- idle-suspend cron (TASK-015 Phase 3) ---
# Same module pattern as gdpr-purge so the operator-side k8s client-go +
# pgx deps stay out of the long-running server binary.
WORKDIR /src/web/onboarding/cmd/idle-suspend
COPY web/onboarding/cmd/idle-suspend/go.mod web/onboarding/cmd/idle-suspend/go.sum ./
RUN go mod download
COPY web/onboarding/cmd/idle-suspend/main.go ./
COPY web/onboarding/cmd/idle-suspend/internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /idle-suspend .

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=go-build /onboarding /onboarding
COPY --from=go-build /gdpr-purge /gdpr-purge
COPY --from=go-build /idle-suspend /idle-suspend
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/onboarding"]
