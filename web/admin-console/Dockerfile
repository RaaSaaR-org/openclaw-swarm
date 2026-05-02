FROM node:22-alpine AS web-build
WORKDIR /web
COPY package.json package-lock.json ./
RUN npm ci
COPY tsconfig.json vite.config.ts index.html ./
COPY src ./src
RUN npm run build

FROM golang:1.25-alpine AS go-build
WORKDIR /src
COPY server/go.mod server/go.sum ./
RUN go mod download
COPY server/*.go ./
COPY --from=web-build /web/dist ./web
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /admin-console .

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=go-build /admin-console /admin-console
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/admin-console"]
