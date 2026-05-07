# Build context is the swarm repo root (so the COPY commands can reach the
# sibling web/shared/ branding contract).
# CI/release wire this up via -f web/status-page/Dockerfile + context: .

FROM node:22-alpine AS web-build
WORKDIR /web
COPY web/shared ./shared
WORKDIR /web/status-page
COPY web/status-page/package.json web/status-page/package-lock.json ./
RUN npm ci
COPY web/status-page/tsconfig.json web/status-page/vite.config.ts web/status-page/index.html ./
COPY web/status-page/src ./src
RUN npm run build

FROM golang:1.25-alpine AS go-build
WORKDIR /src/web/status-page/server
COPY web/status-page/server/go.mod web/status-page/server/go.sum ./
RUN go mod download
COPY web/status-page/server/*.go ./
COPY --from=web-build /web/status-page/dist ./web
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /status-page .

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=go-build /status-page /status-page
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/status-page"]
