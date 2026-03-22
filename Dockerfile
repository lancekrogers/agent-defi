FROM golang:1.24-alpine AS builder
RUN apk add --no-cache git ca-certificates
WORKDIR /src
# Copy agent-coordinator first (required by go.mod replace directive)
COPY agent-coordinator/go.mod agent-coordinator/go.sum /agent-coordinator/
COPY agent-coordinator/ /agent-coordinator/
# Copy agent-defi
COPY agent-defi/go.mod agent-defi/go.sum ./
RUN go mod download
COPY agent-defi/ .
RUN CGO_ENABLED=0 GOOS=linux go build -o /src/bin/agent-defi ./cmd/agent-defi

FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata procps
COPY --from=builder /src/bin/agent-defi /usr/local/bin/agent-defi
ENTRYPOINT ["agent-defi"]
