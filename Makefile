# Same flags as the Docker build: -trimpath drops absolute paths (and the
# username in them); -s -w drops the symbol table and DWARF.
BINARY := requestbin

.PHONY: all build run test clean

all: build

build:
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o $(BINARY) .

run: build
	./$(BINARY)

test:
	go test ./...

clean:
	rm -f $(BINARY)
