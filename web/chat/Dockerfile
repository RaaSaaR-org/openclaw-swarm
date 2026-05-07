# Build context is the swarm repo root (so the COPY commands can reach the
# sibling pkg/ module that go.mod's replace directive points to, and the
# sibling web/shared/ branding contract).
# CI/release wire this up via -f web/chat/Dockerfile + context: .

FROM node:22-alpine AS web-build
WORKDIR /web
COPY web/shared ./shared
WORKDIR /web/chat
COPY web/chat/package.json web/chat/package-lock.json ./
RUN npm ci
COPY web/chat/tsconfig.json web/chat/vite.config.ts web/chat/index.html ./
COPY web/chat/src ./src
RUN npm run build

FROM golang:1.25-alpine AS go-build
WORKDIR /src
# Sibling pkg/ module (replace target) must exist before go mod download runs.
COPY pkg/ ./pkg/
WORKDIR /src/web/chat/server
COPY web/chat/server/go.mod web/chat/server/go.sum ./
RUN go mod download
COPY web/chat/server/*.go ./
COPY --from=web-build /web/chat/dist ./web
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /chat .

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=go-build /chat /chat
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/chat"]
