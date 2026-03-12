VERSION ?= $(shell git describe --tags --always 2>/dev/null || echo "v0.2.0")
BINARY  := ioswarm-agent
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: build release clean

build:
	go build -ldflags "$(LDFLAGS)" -o $(BINARY) .

release: clean
	mkdir -p dist
	GOOS=linux  GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o dist/$(BINARY)-linux-amd64 .
	GOOS=linux  GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o dist/$(BINARY)-linux-arm64 .
	GOOS=darwin GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o dist/$(BINARY)-darwin-amd64 .
	GOOS=darwin GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o dist/$(BINARY)-darwin-arm64 .
	@echo "Built $(VERSION):"
	@ls -lh dist/

clean:
	rm -rf dist $(BINARY)
