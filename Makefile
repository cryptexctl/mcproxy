GOOS?=linux
GOARCH?=amd64
BIN?=mcproxy
VERSION?=$(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

build:
	CGO_ENABLED=0 GOOS=$(GOOS) GOARCH=$(GOARCH) go build -ldflags "-s -w -X 'main.version=$(VERSION)'" -o $(BIN) 