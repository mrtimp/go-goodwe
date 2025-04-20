FROM golang:1.24-alpine AS builder

WORKDIR /app
COPY . .
ARG TARGETOS
ARG TARGETARCH
RUN GOOS=$TARGETOS GOARCH=$TARGETARCH go build -o go-goodwe main.go

FROM alpine:3.20
RUN apk add --no-cache tzdata
COPY --from=builder /app/go-goodwe /app/go-goodwe
ENTRYPOINT ["/app/go-goodwe"]
