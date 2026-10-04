# Web templates and static files are compiled in via //go:embed all:web.
# The process listens on :8080 (all interfaces) unless PORT or flags override it,
# and creates data/bins.json under the working directory.
# Optional retention and capacity (unset keeps the defaults):
#   BIN_TTL=48h
#   MAX_BINS=200
#   MAX_REQUESTS_PER_BIN=100
#   MAX_BODY_BYTES=1048576
# Compile on the builder (BUILDPLATFORM) for each target so linux/amd64 and
# linux/arm64 can be published together without emulating the Go toolchain.
FROM --platform=$BUILDPLATFORM golang:1.27.1 AS build

ARG TARGETOS
ARG TARGETARCH

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
# debug/elf reads the binary; ldd only understands the builder's architecture.
COPY <<'EOF' /opt/checkstatic/checkstatic.go
package main

import (
	"debug/elf"
	"fmt"
	"os"
)

func main() {
	f, err := elf.Open(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer f.Close()
	for _, prog := range f.Progs {
		if prog.Type == elf.PT_INTERP {
			fmt.Fprintln(os.Stderr, "requestbin is dynamically linked")
			os.Exit(1)
		}
	}
}
EOF
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w" -o /out/requestbin . \
    && cd /opt/checkstatic && go mod init checkstatic && go run . /out/requestbin

FROM scratch
COPY --from=build /out/requestbin /requestbin
WORKDIR /app
VOLUME ["/app/data"]
EXPOSE 8080
LABEL org.opencontainers.image.source="https://github.com/CicerBro/requestbin"
ENTRYPOINT ["/requestbin"]
