FROM golang:1.26-alpine AS builder

RUN apk add --no-cache gcc musl-dev git sqlite-dev

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=1 go build -ldflags="-s -w" -o /nxd ./cmd/nxd

FROM alpine:3.20

RUN apk add --no-cache ca-certificates git tmux sqlite-libs

COPY --from=builder /nxd /usr/local/bin/nxd

RUN mkdir -p /root/.nxd

ENTRYPOINT ["nxd"]
CMD ["--help"]
