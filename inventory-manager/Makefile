BINARY := ~/.local/bin/inventory-manager

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
	go build -o $(BINARY) ./cmd/inventory-manager

# Uses FyneApp.toml, including the fyneDo migration metadata, when packaging.
package:
	fyne package -os darwin -name "Image Browser"

clean:
	rm -f $(BINARY)
