BINARY := ~/.local/bin/site-scraper

.PHONY: all fmt tidy test build

all: fmt tidy test build

vet:
	go vet ./...

staticcheck:
	staticcheck ./...

golangci-lint:
	golangci-lint run

fmt:
	fd -a -t f -e go -x gofmt -w {}

tidy:
	go mod tidy

test:
	go test ./...

build:
	go build -o $(BINARY) ./cmd/site-scraper/

clean:
	rm -f $(BINARY)
