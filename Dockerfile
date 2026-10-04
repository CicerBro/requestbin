# Web templates and static files are compiled in via //go:embed all:web.
# The process listens on :8080 (all interfaces) unless PORT or flags override it,
# and creates data/bins.json under the working directory.
FROM golang:1.27.1 AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/requestbin . \
    && ldd /out/requestbin 2>&1 | grep -q 'not a dynamic executable'

FROM scratch
COPY --from=build /out/requestbin /requestbin
WORKDIR /app
VOLUME ["/app/data"]
EXPOSE 8080
LABEL org.opencontainers.image.source="https://github.com/CicerBro/requestbin"
ENTRYPOINT ["/requestbin"]
