# Build context is the swarm repo root (so the COPY commands can reach the
# sibling web/shared/ branding contract).
# CI/release wire this up via -f web/admin-console/Dockerfile + context: .

FROM node:22-alpine AS web-build
WORKDIR /web
COPY web/shared ./shared
WORKDIR /web/admin-console
COPY web/admin-console/package.json web/admin-console/package-lock.json ./
RUN npm ci
COPY web/admin-console/tsconfig.json web/admin-console/vite.config.ts web/admin-console/index.html ./
COPY web/admin-console/src ./src
RUN npm run build

FROM golang:1.25-alpine AS go-build
WORKDIR /src/web/admin-console/server
COPY web/admin-console/server/go.mod web/admin-console/server/go.sum ./
RUN go mod download
COPY web/admin-console/server/*.go ./
COPY --from=web-build /web/admin-console/dist ./web
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /admin-console .

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=go-build /admin-console /admin-console
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/admin-console"]
