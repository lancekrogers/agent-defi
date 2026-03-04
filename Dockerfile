FROM golang:1.24-alpine AS builder
RUN apk add --no-cache git ca-certificates
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /src/bin/agent-defi ./cmd/agent-defi

FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata procps
COPY --from=builder /src/bin/agent-defi /usr/local/bin/agent-defi
ENTRYPOINT ["agent-defi"]
