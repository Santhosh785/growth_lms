# Single image for both the API server and worker; the entrypoint command
# (serve|worker) selects the mode. See Makefile / docker-compose.yml.

# --- Build stage -----------------------------------------------------------
FROM golang:1.25.12-alpine AS build
WORKDIR /src

RUN apk add --no-cache ca-certificates

COPY go.mod go.sum ./
RUN go mod download

COPY cmd ./cmd
COPY internal ./internal

RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/app ./cmd/app

# --- Development stage (used by docker-compose.yml, has hot-reload) -------
FROM golang:1.25.12-alpine AS development
WORKDIR /app
RUN go install github.com/air-verse/air@latest
COPY go.mod go.sum ./
RUN go mod download
COPY . .
EXPOSE 8080
CMD ["air", "-c", ".air.toml"]

# --- Production stage (minimal runtime image) ------------------------------
FROM alpine:3.20 AS production

RUN apk add --no-cache ca-certificates && \
    addgroup -S app && adduser -S app -G app

COPY --from=build /out/app /usr/local/bin/app
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt

USER app
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/app"]
CMD ["serve"]
