# Multi-stage build for AWS Lambda-compatible container image
# ---- Builder Stage ----
FROM golang:1.24 AS builder
WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o /out/archeio ./cmd/archeio

# ---- Final Stage ----
# This stage creates the final, lean image for Lambda.
FROM public.ecr.aws/lambda/provided:al2023
COPY --from=public.ecr.aws/datadog/lambda-extension:latest /opt/. /opt/
COPY --from=builder /out/archeio ./archeio
ENV DD_SERVICE=dd-registry
ENTRYPOINT ["./archeio"]
