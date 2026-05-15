.PHONY: build dev test lint clean install release fetch-model

VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
DATE := $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
LDFLAGS := -ldflags="-s -w \
	-X github.com/achetronic/baifo/internal/version.tag=$(VERSION) \
	-X github.com/achetronic/baifo/internal/version.commit=$(COMMIT) \
	-X github.com/achetronic/baifo/internal/version.date=$(DATE)"

# Main development commands
build:
	@echo "Building baifo..."
	@mkdir -p bin
	go build $(LDFLAGS) -o bin/baifo ./cmd/baifo

dev:
	@echo "Running in dev mode..."
	go run $(LDFLAGS) ./cmd/baifo

test:
	@echo "Running tests..."
	go test -v -race ./...

lint:
	@echo "Running linter..."
	golangci-lint run

clean:
	@echo "Cleaning up..."
	@rm -rf bin/ dist/

install: build
	@echo "Installing baifo to $(GOPATH)/bin..."
	@cp bin/baifo $(GOPATH)/bin/baifo

# Cross-compilation for releases
release:
	@echo "Building cross-compiled releases..."
	@mkdir -p dist
	GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o dist/baifo-linux-amd64 ./cmd/baifo
	GOOS=linux GOARCH=arm64 go build $(LDFLAGS) -o dist/baifo-linux-arm64 ./cmd/baifo
	GOOS=darwin GOARCH=amd64 go build $(LDFLAGS) -o dist/baifo-darwin-amd64 ./cmd/baifo
	GOOS=darwin GOARCH=arm64 go build $(LDFLAGS) -o dist/baifo-darwin-arm64 ./cmd/baifo
	GOOS=windows GOARCH=amd64 go build $(LDFLAGS) -o dist/baifo-windows-amd64.exe ./cmd/baifo
	GOOS=windows GOARCH=arm64 go build $(LDFLAGS) -o dist/baifo-windows-arm64.exe ./cmd/baifo

# Embedding model generation
# We download the raw safetensors model from Hugging Face and quantize it to int8
# locally using a Python script, rather than storing the 547MB raw file in the repo.
# The resulting .weights file is what baifo embeds at compile time.
EMBED_ASSETS := internal/embeddings/assets
EMBED_TOOLS := internal/embeddings/tools
MODEL_URL := https://huggingface.co/nomic-ai/nomic-embed-text-v1.5/resolve/main/model.safetensors
VOCAB_URL := https://huggingface.co/nomic-ai/nomic-embed-text-v1.5/resolve/main/vocab.txt

fetch-model:
	@echo "Checking for Python and numpy..."
	@python3 -c "import numpy" 2>/dev/null || (echo "Error: Python with numpy is required for quantization (run: pip install numpy)" && exit 1)
	@mkdir -p $(EMBED_ASSETS)
	@echo "Downloading vocab..."
	@curl -sL --max-time 60 $(VOCAB_URL) -o $(EMBED_ASSETS)/vocab.txt
	@echo "Downloading model weights (this might take a while)..."
	@curl -L --max-time 1200 $(MODEL_URL) -o $(EMBED_TOOLS)/model.tmp
	@echo "Quantizing model to int8..."
	@python3 $(EMBED_TOOLS)/convert.py $(EMBED_TOOLS)/model.tmp $(EMBED_ASSETS)/nomic-embed-text.weights
	@echo "Cleaning up temp files..."
	@rm -f $(EMBED_TOOLS)/model.tmp
	@echo "Model downloaded and quantized successfully."
