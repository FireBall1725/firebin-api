# syntax=docker/dockerfile:1

# ── Build stage ───────────────────────────────────────────────────────────────
# Pinned to the BUILD platform: without this, buildx runs the whole arm64
# leg under QEMU emulation. Go cross-compiles natively.
FROM --platform=$BUILDPLATFORM golang:1.26 AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Empty, not "dev". internal/version treats an empty Version as a local build
# and computes "{YY}.{M}.DEV" from the date; injecting the literal "dev" would
# short-circuit that and report "dev" forever.
ARG VERSION=
# Supplied by buildx per platform leg.
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} go build \
    -ldflags "-s -w -X github.com/firelabsca/firebin-api/internal/version.Version=${VERSION}" \
    -o /out/firebin-api ./cmd/api

# ── Runtime stage ─────────────────────────────────────────────────────────────
FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /out/firebin-api /app/firebin-api
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/app/firebin-api"]
