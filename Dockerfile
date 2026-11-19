# syntax=docker/dockerfile:1

# Build the static Go binary. CGO is disabled so the runtime image carries no
# libc toolchain and the binary matches `go build ./...` in CI.
FROM golang:1.25-bookworm AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath -ldflags="-s -w" \
    -o /out/server ./cmd/server

# Runtime: certificates for HTTPS (Expo, JWKS, S3/R2) and tzdata because push
# quiet hours resolve IANA zones such as Africa/Nairobi.
FROM debian:bookworm-slim
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates tzdata \
    && rm -rf /var/lib/apt/lists/* \
    && useradd --system --uid 10001 --create-home --shell /usr/sbin/nologin app

WORKDIR /app
COPY --from=build /out/server /app/server

USER app
ENV PORT=8080
EXPOSE 8080

ENTRYPOINT ["/app/server"]
