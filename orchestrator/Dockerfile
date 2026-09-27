# orchestrator (gateway) — build context: repository root (needs contracts/)
FROM golang:1.26.8-alpine3.24 AS builder
WORKDIR /src
COPY contracts/ contracts/
COPY orchestrator/go.mod orchestrator/go.sum orchestrator/
WORKDIR /src/orchestrator
RUN go mod download
COPY orchestrator/ .
RUN CGO_ENABLED=0 go build -trimpath -o /out/gateway ./cmd/orchestrator

FROM alpine:3.24.2
RUN adduser -D -H -u 10001 app
WORKDIR /app
COPY --from=builder /out/gateway ./gateway
USER app
EXPOSE 8080
ENTRYPOINT ["./gateway"]
