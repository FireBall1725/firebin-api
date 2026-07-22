# syntax=docker/dockerfile:1

# ── Build stage ───────────────────────────────────────────────────────────────
FROM golang:1.26 AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags "-s -w -X github.com/firelabsca/firebin-api/internal/version.Version=${VERSION}" \
    -o /out/firebin-api ./cmd/api

# ── Runtime stage ─────────────────────────────────────────────────────────────
FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /out/firebin-api /app/firebin-api
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/app/firebin-api"]
