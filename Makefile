.PHONY: build test cover verify install

build:
	go build -o bin/vivero ./cmd/vivero

test:
	go test ./...

cover:
	go test -coverprofile=/tmp/vivero-cover.out ./...
	go tool cover -func=/tmp/vivero-cover.out

verify:
	gofmt -w cmd internal skills
	go test ./...
	go build -o bin/vivero ./cmd/vivero

install:
	go install ./cmd/vivero
