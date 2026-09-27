# grades_ingest_service — build context: repository root (needs contracts/)
FROM golang:1.26.8-alpine3.24 AS builder
WORKDIR /src
COPY contracts/ contracts/
COPY grades_ingest_service/go.mod grades_ingest_service/go.sum grades_ingest_service/
WORKDIR /src/grades_ingest_service
RUN go mod download
COPY grades_ingest_service/ .
RUN CGO_ENABLED=0 go build -trimpath -o /out/service ./cmd/grades-ingest

FROM alpine:3.24.2
RUN adduser -D -H -u 10001 app
USER app
COPY --from=builder /out/service /app/service
EXPOSE 8080
ENTRYPOINT ["/app/service"]
