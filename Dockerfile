# syntax=docker/dockerfile:1

# ── build ────────────────────────────────────────────────────────────────────
FROM golang:1.26-alpine AS build

WORKDIR /src

# Dependencies are copied first so the module download layer is reused whenever
# only application code changes.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# CGO_ENABLED=0 produces a static binary, which is what lets the runtime stage
# be a distroless image with no libc at all.
# -trimpath keeps build paths out of the binary; -s -w drops the symbol table.
RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w" \
    -o /out/api ./cmd/api

# ── runtime ──────────────────────────────────────────────────────────────────
# distroless/static has no shell, no package manager and no libc: nothing to
# exploit and nothing to patch. It ships the CA bundle, which is required for
# TLS to managed Postgres.
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/api /api

# Never root. Cloud Run does not require it, and there is no reason to.
USER nonroot:nonroot

# Documentation only — the process binds whatever PORT the platform injects.
EXPOSE 8080

ENTRYPOINT ["/api"]
