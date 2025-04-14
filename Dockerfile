FROM golang:1.24-alpine as builder

WORKDIR /app
COPY . .

RUN go mod tidy && go build -o omeka_exporter

FROM alpine:latest
WORKDIR /app
COPY --from=builder /app/omeka_exporter /omeka_exporter

EXPOSE 9145
ENTRYPOINT ["/omeka_exporter"]