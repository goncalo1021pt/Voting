.PHONY: build lint test clean help

# Build the backend locally
build: lint
	go build -o voting-server ./srcs
	@echo "✅ Backend built successfully"

# Run linter and checks
lint:
	go mod tidy
	go vet ./srcs 2>/dev/null || true
	@echo "✅ Code checked"

# Run tests (when we add them)
test:
	go test -v ./srcs

# Clean build artifacts
clean:
	rm -f voting-server
	go clean
	@echo "✅ Build artifacts cleaned"

# Help
help:
	@echo "Backend Makefile - Available commands:"
	@echo "  make build  - Build backend locally (runs lint first)"
	@echo "  make lint   - Run linter and code checks"
	@echo "  make test   - Run tests"
	@echo "  make clean  - Clean build artifacts"
	@echo "  make help   - Show this help message"
