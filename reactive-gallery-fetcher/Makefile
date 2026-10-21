BINARY := ~/.local/bin/reactive-gallery-fetcher

.PHONY: all fmt tidy test build

all: fmt tidy test build

fmt:
	fd -a -t f -e go -x gofmt -w {}

tidy:
	go mod tidy

test:
	go test ./...

vet:
	go vet ./...

staticcheck:
	staticcheck ./...

golangci-lint:
	golangci-lint run

build:
	go build -o $(BINARY) ./main.go

clean:
	rm -f $(BINARY)
