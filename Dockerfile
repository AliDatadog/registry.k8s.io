# Build and run archeio

FROM golang:1.24 AS builder
WORKDIR /app
COPY . .
RUN go mod download
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o archeio ./cmd/archeio

FROM alpine:latest
COPY --from=builder /app/archeio /app/
COPY --from=builder /app/data /app/data/
WORKDIR /app
EXPOSE 8080
CMD ["./archeio", "-v=2"]