# voxthief — the Makefile is the only supported build entry point (§13).
# Prerequisites: Go 1.26+, cmake, a C/C++ toolchain (Xcode CLT / gcc /
# MSYS2 mingw-w64). cgo does not use MSVC on Windows.

MODULE  := github.com/pid1/voxthief
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
WHISPER_DIR := third_party/whisper.cpp
WHISPER_LIB := $(WHISPER_DIR)/build/src/libwhisper.a

# Metal on darwin (free at build time, §15.4); CPU elsewhere.
UNAME_S := $(shell uname -s)
ifeq ($(UNAME_S),Darwin)
  GGML_METAL := ON
else
  GGML_METAL := OFF
endif

LDFLAGS := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT)

.PHONY: all build whisper test lint generate fmt clean tidy vet

all: build

## whisper: static build of the vendored whisper.cpp (cmake, Metal ON on darwin)
whisper: $(WHISPER_LIB)

$(WHISPER_LIB):
	@if [ ! -f "$(WHISPER_DIR)/CMakeLists.txt" ]; then \
		echo "whisper.cpp submodule missing; run: git submodule update --init --recursive"; \
		exit 1; \
	fi
	cmake -S $(WHISPER_DIR) -B $(WHISPER_DIR)/build \
		-DBUILD_SHARED_LIBS=OFF \
		-DGGML_METAL=$(GGML_METAL) \
		-DGGML_OPENMP=OFF \
		-DWHISPER_BUILD_TESTS=OFF \
		-DWHISPER_BUILD_EXAMPLES=OFF \
		-DCMAKE_BUILD_TYPE=Release
	cmake --build $(WHISPER_DIR)/build --config Release -j
	@# MinGW's cmake emits the ggml archives with no `lib` prefix (libwhisper.a
	@# keeps one), so -lggml/-lggml-cpu/-lggml-base find nothing and the link
	@# fails on windows only. Add prefixed copies; a no-op where cmake already
	@# writes the prefix, so darwin and linux are unaffected.
	@for d in $(WHISPER_DIR)/build/src $(WHISPER_DIR)/build/ggml/src; do \
		for f in "$$d"/*.a; do \
			[ -f "$$f" ] || continue; \
			b=$$(basename "$$f"); \
			case "$$b" in lib*) continue ;; esac; \
			cp -f "$$f" "$$d/lib$$b"; \
		done; \
	done

## build: build the binary with whisper linked in (CGO)
build: whisper
	CGO_ENABLED=1 go build -tags whisper -ldflags "$(LDFLAGS)" -o voxthief ./cmd/voxthief

## build-nowhisper: capture-only build without the C toolchain (dev convenience)
build-nowhisper:
	go build -ldflags "$(LDFLAGS)" -o voxthief ./cmd/voxthief

## test: run the full suite under the race detector
test:
	go test -race ./...

## test-integration: ASR integration tests (needs whisper + cached models)
test-integration: whisper
	CGO_ENABLED=1 go test -race -tags "whisper integration" ./internal/asr/...

## vet: go vet
vet:
	go vet ./...

## lint: golangci-lint (install: https://golangci-lint.run)
lint:
	golangci-lint run ./...

## fmt: format with gofumpt (falls back to gofmt)
fmt:
	@command -v gofumpt >/dev/null 2>&1 && gofumpt -w . || gofmt -w .

## generate: regenerate sqlc code (requires sqlc)
generate:
	sqlc generate

## tidy: tidy go.mod/go.sum
tidy:
	go mod tidy

clean:
	rm -f voxthief
	rm -rf $(WHISPER_DIR)/build
