FROM golang:1.23-alpine AS builder
WORKDIR /app
ARG VERSION=dev
ENV CGO_ENABLED=0

COPY go.mod ./
RUN go mod download
COPY . ./
RUN go build -trimpath -ldflags="-s -w -X main.version=$VERSION" -o pico-tracker

FROM alpine:latest AS runner
WORKDIR /app
ENV PICO_TRACKER__PORT=1337

RUN apk add --no-cache netcat-openbsd
COPY --from=builder /app/pico-tracker ./

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
	CMD sh -c 'printf packet_16_length | nc -u -w1 127.0.0.1 $PICO_TRACKER__PORT | grep -q .'
ENTRYPOINT ["/app/pico-tracker"]
