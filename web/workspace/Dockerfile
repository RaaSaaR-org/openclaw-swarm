# Build context is the swarm repo root (so the COPY commands can reach the
# sibling pkg/ module that go.mod's replace directive points to, and the
# sibling web/shared/ branding contract).
# CI/release wire this up via -f web/workspace/Dockerfile + context: .

FROM node:22-alpine AS web-build
WORKDIR /web
COPY web/shared ./shared
WORKDIR /web/workspace
COPY web/workspace/package.json web/workspace/package-lock.json ./
RUN npm ci
COPY web/workspace/tsconfig.json web/workspace/vite.config.ts web/workspace/index.html ./
COPY web/workspace/src ./src
RUN npm run build

FROM golang:1.25-alpine AS go-build
WORKDIR /src
# Sibling pkg/ module (replace target) must exist before go mod download runs.
COPY pkg/ ./pkg/
WORKDIR /src/web/workspace/server
COPY web/workspace/server/go.mod web/workspace/server/go.sum ./
RUN go mod download
COPY web/workspace/server/*.go ./
COPY --from=web-build /web/workspace/dist ./web
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /workspace .

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=go-build /workspace /workspace
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/workspace"]
