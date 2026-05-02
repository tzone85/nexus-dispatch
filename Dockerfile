# syntax=docker/dockerfile:1.7
ARG GO_VERSION=1.26
ARG ALPINE_VERSION=3.20.3

# ---- builder ----
FROM golang:${GO_VERSION}-alpine AS builder

RUN apk add --no-cache gcc musl-dev git sqlite-dev

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# RELEASE flag controls whether debug symbols are stripped. Default is to
# KEEP symbols so crash dumps are useful. Pass `--build-arg RELEASE=1`
# (set by `make release`) to strip for the smaller release binary.
ARG RELEASE=0
ARG VERSION=dev
RUN if [ "$RELEASE" = "1" ]; then \
      CGO_ENABLED=1 go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /nxd ./cmd/nxd; \
    else \
      CGO_ENABLED=1 go build -ldflags="-X main.version=${VERSION}" -o /nxd ./cmd/nxd; \
    fi

# ---- runtime ----
FROM alpine:${ALPINE_VERSION}

RUN apk add --no-cache ca-certificates git tmux sqlite-libs && \
    addgroup -S nxd && adduser -S -G nxd nxd

COPY --from=builder /nxd /usr/local/bin/nxd

RUN mkdir -p /home/nxd/.nxd && chown -R nxd:nxd /home/nxd

USER nxd
WORKDIR /home/nxd

ENTRYPOINT ["nxd"]
CMD ["--help"]
