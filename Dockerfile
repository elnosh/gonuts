FROM golang:1.22.2-alpine AS builder

# for gcc
RUN apk add build-base

WORKDIR /app
COPY . /app

# need CGO for sqlite
RUN CGO_ENABLED=1 GOOS=linux go build -o main ./cmd/mint/mint.go

FROM alpine:latest AS final

COPY --from=builder /app .

EXPOSE 3338
CMD ["./main"]
