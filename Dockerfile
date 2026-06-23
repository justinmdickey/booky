# ---- build ----
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# Version is stamped from the build arg (CI passes the git tag).
ARG VERSION=dev
# Pure-Go (modernc sqlite), so CGO stays off -> tiny static binary.
RUN CGO_ENABLED=0 go build -trimpath \
    -ldflags="-s -w -X github.com/justindickey/booky/internal/version.Version=${VERSION}" \
    -o /booky ./cmd/booky

# ---- run ----
FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata && adduser -D -u 1000 booky \
    && mkdir -p /data && chown booky:booky /data
COPY --from=build /booky /usr/local/bin/booky
USER booky
ENV BOOKY_ADDR=:8222 \
    BOOKY_DATA_DIR=/data
VOLUME ["/data"]
EXPOSE 8222
ENTRYPOINT ["/usr/local/bin/booky"]
