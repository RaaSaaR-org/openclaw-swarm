# Build context is the swarm repo root (so the COPY commands can reach the
# sibling pkg/ module that go.mod's replace directive points to).
# CI/release wire this up via -f web/customer-chat/Dockerfile + context: .

FROM node:22-alpine AS web-build
WORKDIR /web
COPY web/customer-chat/package.json web/customer-chat/package-lock.json ./
RUN npm ci
COPY web/customer-chat/tsconfig.json web/customer-chat/vite.config.ts web/customer-chat/index.html ./
COPY web/customer-chat/src ./src
RUN npm run build

FROM golang:1.25-alpine AS go-build
WORKDIR /src
# Sibling pkg/ module (replace target) must exist before go mod download runs.
COPY pkg/ ./pkg/
WORKDIR /src/web/customer-chat/server
COPY web/customer-chat/server/go.mod web/customer-chat/server/go.sum ./
RUN go mod download
COPY web/customer-chat/server/*.go ./
COPY --from=web-build /web/dist ./web
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /customer-chat .

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=go-build /customer-chat /customer-chat
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/customer-chat"]
