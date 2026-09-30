BINARY := image-browser

.PHONY: all build run test tidy clean package

all: build

build:
	go build -o $(BINARY) ./cmd/image-browser

run:
	go run ./cmd/image-browser view

test:
	go test ./...

tidy:
	go mod tidy

# Uses FyneApp.toml, including the fyneDo migration metadata, when packaging.
package:
	fyne package -os darwin -name "Image Browser"

clean:
	rm -f $(BINARY)
