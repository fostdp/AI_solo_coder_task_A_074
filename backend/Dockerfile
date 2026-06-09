FROM golang:1.21-alpine AS builder

RUN apk add --no-cache git ca-certificates

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags="-s -w -extldflags '-static'" \
    -tags "netgo osusergo" \
    -o /lng-monitor ./cmd/

FROM scratch

COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /build/configs/model_params.json /app/configs/model_params.json
COPY --from=builder /lng-monitor /app/lng-monitor

EXPOSE 8080 6060

ENTRYPOINT ["/app/lng-monitor"]
