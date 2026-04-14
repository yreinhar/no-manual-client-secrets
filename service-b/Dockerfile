FROM golang:1.23-alpine AS builder
WORKDIR /app
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o service-b .

FROM scratch
COPY --from=builder /app/service-b /service-b
EXPOSE 8080
ENTRYPOINT ["/service-b"]
