# institutions_service — build context: repository root (needs contracts/)
FROM golang:1.26.8-alpine3.24 AS builder
WORKDIR /src
COPY contracts/ contracts/
COPY institutions_service/go.mod institutions_service/go.sum institutions_service/
WORKDIR /src/institutions_service
RUN go mod download
COPY institutions_service/ .
RUN CGO_ENABLED=0 go build -trimpath -o /out/service ./cmd/institutions

FROM alpine:3.24.2
RUN adduser -D -H -u 10001 app
USER app
COPY --from=builder /out/service /app/service
EXPOSE 8080
ENTRYPOINT ["/app/service"]
