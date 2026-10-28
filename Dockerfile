FROM golang:1.25-bookworm AS build
WORKDIR /src
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /cornwatch ./cmd/cornwatch

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates && rm -rf /var/lib/apt/lists/* && useradd --uid 10001 --create-home cornwatch && mkdir /data && chown cornwatch:cornwatch /data
COPY --from=build /cornwatch /usr/local/bin/cornwatch
USER cornwatch
VOLUME ["/data"]
EXPOSE 8765
ENTRYPOINT ["cornwatch"]
CMD ["--addr", "0.0.0.0:8765", "--data", "/data/cornwatch.db", "--ui", "", "--demo=false"]
