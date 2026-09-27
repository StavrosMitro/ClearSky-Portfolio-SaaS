# identity_service — build context: repository root (needs contracts/)
FROM golang:1.26.8-alpine3.24 AS builder
WORKDIR /src
COPY contracts/ contracts/
COPY identity_service/go.mod identity_service/go.sum identity_service/
WORKDIR /src/identity_service
RUN go mod download
COPY identity_service/ .
RUN CGO_ENABLED=0 go build -trimpath -o /out/service ./cmd/identity

FROM alpine:3.24.2
# CA certificates for Google's HTTPS endpoints.
RUN apk add --no-cache ca-certificates && adduser -D -H -u 10001 app
USER app
COPY --from=builder /out/service /app/service
EXPOSE 8080
ENTRYPOINT ["/app/service"]
