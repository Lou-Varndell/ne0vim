.PHONY: all fmt tidy test build

all: fmt tidy test

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
