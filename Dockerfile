# Multi-stage build so the runtime image is tiny.
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags '-s -w' -o /app/markethouse-api ./cmd/server

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
WORKDIR /app
COPY --from=build /app/markethouse-api .
RUN mkdir -p uploads/profile uploads/header uploads/posts uploads/supply uploads/chat uploads/status
EXPOSE 8080
CMD ["/app/markethouse-api"]
