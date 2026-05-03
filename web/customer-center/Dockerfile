# Build context is the swarm repo root (so the COPY commands can reach the
# sibling pkg/ module that go.mod's replace directive points to).
# CI/release wire this up via -f web/customer-center/Dockerfile + context: .

FROM node:22-alpine AS web-build
WORKDIR /web
COPY web/customer-center/package.json web/customer-center/package-lock.json ./
RUN npm ci
COPY web/customer-center/tsconfig.json web/customer-center/vite.config.ts web/customer-center/index.html ./
COPY web/customer-center/src ./src
RUN npm run build

FROM golang:1.25-alpine AS go-build
WORKDIR /src
# Sibling pkg/ module (replace target) must exist before go mod download runs.
COPY pkg/ ./pkg/
WORKDIR /src/web/customer-center/server
COPY web/customer-center/server/go.mod web/customer-center/server/go.sum ./
RUN go mod download
COPY web/customer-center/server/*.go ./
COPY --from=web-build /web/dist ./web
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /customer-center .

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=go-build /customer-center /customer-center
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/customer-center"]
