# --- Build stage ---
# syntax=docker/dockerfile:1
FROM golang:1.26-alpine AS builder

RUN apk add --no-cache git

WORKDIR /src

# Cache dependencies
COPY server/go.mod server/go.sum ./server/
RUN --mount=type=cache,target=/go/pkg/mod \
    cd server && go mod download

# Copy server source
COPY server/ ./server/

# Build binaries
# --mount=type=cache persists the Go build cache between Docker builds (~98% faster rebuilds).
# Separate RUN instructions allow Docker to cache each binary layer independently.
ARG VERSION=dev
ARG COMMIT=unknown
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    cd server && CGO_ENABLED=0 go build -ldflags "-s -w -X main.version=${VERSION} -X main.commit=${COMMIT}" -o bin/server ./cmd/server
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    cd server && CGO_ENABLED=0 go build -ldflags "-s -w -X main.version=${VERSION} -X main.commit=${COMMIT}" -o bin/multica ./cmd/multica
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    cd server && CGO_ENABLED=0 go build -ldflags "-s -w" -o bin/migrate ./cmd/migrate
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    cd server && CGO_ENABLED=0 go build -ldflags "-s -w" -o bin/backfill_task_usage_hourly ./cmd/backfill_task_usage_hourly

# --- Runtime stage ---
FROM alpine:3.21

RUN apk add --no-cache ca-certificates tzdata

WORKDIR /app

COPY --from=builder /src/server/bin/server .
COPY --from=builder /src/server/bin/multica .
COPY --from=builder /src/server/bin/migrate .
COPY --from=builder /src/server/bin/backfill_task_usage_hourly .
COPY server/migrations/ ./migrations/
COPY docker/entrypoint.sh .
RUN sed -i 's/\r$//' entrypoint.sh && chmod +x entrypoint.sh

EXPOSE 8080

ENTRYPOINT ["./entrypoint.sh"]
