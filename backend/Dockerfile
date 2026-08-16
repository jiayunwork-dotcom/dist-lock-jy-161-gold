FROM golang:1.21-alpine AS builder

WORKDIR /app

ENV GOPROXY=https://goproxy.cn,direct

RUN apk add --no-cache git gcc musl-dev

COPY . .

RUN go mod tidy && CGO_ENABLED=0 GOOS=linux go build -o dist-lock-server ./cmd/main.go

FROM alpine:latest

RUN apk add --no-cache ca-certificates tzdata

WORKDIR /app

COPY --from=builder /app/dist-lock-server /app/dist-lock-server

EXPOSE 8080 7000

ENTRYPOINT ["/app/dist-lock-server"]
