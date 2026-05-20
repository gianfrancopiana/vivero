BINARY := vivero
VERSION ?= dev
COVERPROFILE ?= /tmp/vivero-cover.out
COVER_MIN ?= 72.0
LDFLAGS := -ldflags "-s -w -X github.com/gianfrancopiana/vivero/internal/vivero.Version=$(VERSION)"

.PHONY: build test test-short test-race vet fmt-check cover verify cross-build install snapshot release-smoke example-e2e integration-fixtures deploy-fixtures nasty-integration-fixtures dogfood-configs clean

build:
	go build $(LDFLAGS) -o bin/$(BINARY) ./cmd/vivero

test:
	go test ./...

test-short:
	go test -short ./...

test-race:
	go test -race ./...

vet:
	go vet ./...

fmt-check:
	@files="$$(gofmt -l cmd internal skills)"; \
	if [ -n "$$files" ]; then \
		echo "gofmt needed:"; \
		echo "$$files"; \
		exit 1; \
	fi

cover:
	go test -coverprofile=$(COVERPROFILE) ./...
	go tool cover -func=$(COVERPROFILE) | tee /tmp/vivero-cover-func.out
	@total="$$(awk '/^total:/ {gsub(/%/, "", $$3); print $$3}' /tmp/vivero-cover-func.out)"; \
	awk -v total="$$total" -v min="$(COVER_MIN)" 'BEGIN { if (total + 0 < min + 0) { printf "coverage %.1f%% is below floor %.1f%%\n", total, min; exit 1 } printf "coverage %.1f%% meets floor %.1f%%\n", total, min }'

verify: fmt-check vet test build

cross-build:
	@set -e; \
	for goos in linux darwin; do \
		for goarch in amd64 arm64; do \
			out="/tmp/vivero-$$goos-$$goarch"; \
			echo "building $$goos/$$goarch"; \
			CGO_ENABLED=0 GOOS="$$goos" GOARCH="$$goarch" go build $(LDFLAGS) -o "$$out" ./cmd/vivero; \
		done; \
	done

install:
	go install $(LDFLAGS) ./cmd/vivero

snapshot:
	goreleaser release --snapshot --clean

release-smoke:
	scripts/release-smoke.sh

example-e2e:
	scripts/example-e2e.sh

integration-fixtures:
	scripts/integration-fixtures.sh

deploy-fixtures:
	scripts/deploy-fixtures.sh

nasty-integration-fixtures:
	scripts/nasty-integration-fixtures.sh

dogfood-configs:
	scripts/dogfood-configs.sh

clean:
	rm -rf bin dist
