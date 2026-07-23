BUILD_DIR := build
BINARY    := $(BUILD_DIR)/cliutils

.PHONY: all build install test lint fmt clean

all: lint test build

build:
	@mkdir -p $(BUILD_DIR)
	go build -o $(BINARY) .

install:
	go install .

test:
	go test ./...

lint:
	@test -z "$$(gofmt -l .)" || { echo "unformatted files (run 'make fmt'):"; gofmt -l .; exit 1; }
	go vet ./...

fmt:
	gofmt -w .

clean:
	rm -rf $(BUILD_DIR)
