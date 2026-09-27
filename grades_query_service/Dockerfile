# grades_query_service — build context: repository root (needs contracts/)
FROM golang:1.26.8-alpine3.24 AS builder
WORKDIR /src
COPY contracts/ contracts/
COPY grades_query_service/go.mod grades_query_service/go.sum grades_query_service/
WORKDIR /src/grades_query_service
RUN go mod download
COPY grades_query_service/ .
RUN CGO_ENABLED=0 go build -trimpath -o /out/service ./cmd/grades-query

FROM alpine:3.24.2
RUN adduser -D -H -u 10001 app
USER app
COPY --from=builder /out/service /app/service
EXPOSE 8080
ENTRYPOINT ["/app/service"]
