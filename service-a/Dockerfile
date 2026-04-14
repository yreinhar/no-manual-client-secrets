FROM golang:1.23-alpine AS builder
WORKDIR /app
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o service-a .

FROM scratch
COPY --from=builder /app/service-a /service-a
EXPOSE 8080
ENTRYPOINT ["/service-a"]
