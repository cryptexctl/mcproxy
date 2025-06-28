GOOS?=linux
GOARCH?=amd64
BIN?=mcproxy

build:
	CGO_ENABLED=0 GOOS=$(GOOS) GOARCH=$(GOARCH) go build -o $(BIN) 