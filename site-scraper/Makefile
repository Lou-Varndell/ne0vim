.PHONY: all fmt tidy test build

all: fmt tidy test build

fmt:
	fd -a -t f -e go -x gofmt -w {}

tidy:
	go mod tidy

test:
	go test ./...

build:
	go build -o ~/.local/bin/site-scraper ./cmd/site-scraper/
