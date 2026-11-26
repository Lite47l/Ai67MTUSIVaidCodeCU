FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /pulse ./cmd/server

FROM alpine:3.22
RUN apk add --no-cache ca-certificates && addgroup -g 10001 pulse && adduser -D -u 10001 -G pulse pulse
WORKDIR /app
COPY --from=build /pulse /app/pulse
COPY data/history.json /app/data/history.json
RUN mkdir -p /app/storage && chown -R pulse:pulse /app/storage
USER pulse
ENV ADDR=0.0.0.0:8080 DB_PATH=/app/storage/pulse.db
EXPOSE 8080
HEALTHCHECK --interval=15s --timeout=3s --start-period=10s CMD wget -q -O /dev/null http://127.0.0.1:8080/readyz || exit 1
ENTRYPOINT ["/app/pulse"]
