ARG KOPIA_VERSION=0.18.2

# ── stage 1: build pdbackup ────────────────────────────────────────────────────
FROM golang:1.26-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /pdbackup .

# ── stage 2: download kopia binary ────────────────────────────────────────────
FROM alpine:3.19 AS kopia-dl
ARG KOPIA_VERSION
ARG TARGETARCH

RUN apk add --no-cache curl tar && \
    ARCH="$(case "${TARGETARCH:-amd64}" in amd64) echo x64;; arm64) echo arm64;; *) echo x64;; esac)" && \
    curl -fsSL "https://github.com/kopia/kopia/releases/download/v${KOPIA_VERSION}/kopia-${KOPIA_VERSION}-linux-${ARCH}.tar.gz" \
        -o /tmp/kopia.tar.gz && \
    tar -C /tmp -xzf /tmp/kopia.tar.gz && \
    mv /tmp/kopia-${KOPIA_VERSION}-linux-${ARCH}/kopia /kopia && \
    chmod +x /kopia

# ── stage 3: final image ───────────────────────────────────────────────────────
FROM alpine:3.19
RUN apk add --no-cache ca-certificates

COPY --from=builder  /pdbackup        /usr/local/bin/pdbackup
COPY --from=kopia-dl /kopia           /usr/local/bin/kopia

ENTRYPOINT ["/usr/local/bin/pdbackup"]
