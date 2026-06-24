FROM golang:1.23-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY main.go .
RUN CGO_ENABLED=0 go build -o /sample-app .

FROM alpine:latest
COPY --from=builder /sample-app /sample-app
EXPOSE 8080
CMD ["/sample-app"]
