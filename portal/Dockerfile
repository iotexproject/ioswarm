FROM golang:1.22-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /ioswarm-portal .

FROM alpine:3.19
COPY --from=builder /ioswarm-portal /usr/local/bin/
COPY --from=builder /app/templates /app/templates
WORKDIR /app
ENTRYPOINT ["ioswarm-portal", "--templates=/app/templates"]
