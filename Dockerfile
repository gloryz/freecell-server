FROM golang:1.22-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -o freecell-server .

FROM alpine:latest
RUN apk --no-cache add ca-certificates
WORKDIR /data
COPY --from=builder /app/freecell-server /usr/local/bin/
EXPOSE 8080
CMD ["freecell-server"]
