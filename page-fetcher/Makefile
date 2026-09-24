.PHONY: dev build templ-gen bulma htmx test clean

# Download Bulma CSS (run once, or after clean)
bulma: web/static/css/bulma.min.css
web/static/css/bulma.min.css:
	@mkdir -p web/static/css
	curl -sLo web/static/css/bulma.min.css \
	  "https://cdn.jsdelivr.net/npm/bulma@1.0.2/css/bulma.min.css"
	@echo "Bulma CSS installed"

# Vendor HTMX (run once, or after clean)
htmx: web/static/js/htmx.min.js
web/static/js/htmx.min.js:
	@mkdir -p web/static/js
	curl -sLo web/static/js/htmx.min.js \
	  "https://unpkg.com/htmx.org@2.0.3/dist/htmx.min.js"
	@echo "HTMX v2.0.3 vendored"

# Start dev: templ watch + air
dev: bulma htmx
	@echo "Starting dev server..."
	@templ generate --watch &
	@air; trap 'kill 0' INT

# Production build
build: bulma htmx templ-gen
	go build -o bin/server ./cmd/server

templ-gen:
	templ generate

test:
	go test ./...

clean:
	rm -rf tmp bin web/static/css/bulma.min.css
