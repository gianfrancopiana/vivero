.PHONY: build test verify install

build:
	go build -o bin/vivero ./cmd/vivero

test:
	go test ./...

verify:
	gofmt -w cmd internal skills
	go test ./...
	go build -o bin/vivero ./cmd/vivero

install:
	go install ./cmd/vivero
