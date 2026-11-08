# AGENTS.md

## Sadr

### Build and test
- Run `git submodule update --init --recursive` first. The whisper.cpp source lives in the `third_party/whisper.cpp` submodule.
- `make whisper` builds the static whisper.cpp libraries. It needs cmake and a C/C++ toolchain.
- `go vet ./...` and `golangci-lint run --timeout=5m ./...` pass with 0 issues. CI installs golangci-lint with `go install` at the version pinned in `.github/workflows/ci.yml`.
- `go test -race ./...` runs the unit tests without cgo or whisper.
- `make build && ./voxthief version` builds the cgo binary with whisper linked in and prints its version.
- The ASR integration tests run with `VOXTHIEF_MODELS_DIR=DIR VOXTHIEF_SPEECH_WAV=$PWD/third_party/whisper.cpp/samples/jfk.wav CGO_ENABLED=1 go test -race -tags "whisper integration" ./internal/asr/...`. VOXTHIEF_SPEECH_WAV must be an absolute path, because go test runs in the package folder. DIR holds `ggml-tiny.en.bin`, `ggml-small.en-q8_0.bin` and `ggml-silero-v6.2.0.bin`, from the URLs and SHA-256 sums in `internal/asr/models.go`.
- `VOXTHIEF_NET_TEST=1 go test -run TestEnsureModelRealDownloadVerifies ./internal/asr/` downloads the Silero VAD model from its URL in `internal/asr/models.go` and checks its SHA-256.
- `TestVADTimelineMapping` skips because its synthetic tone yields no segments. `TestVADTimelineMappingRealSpeech` with `jfk.wav` is the check that whisper's VAD timestamps map back to the original audio.
- A Linux host cannot build the darwin-arm64 (Metal) target. The CI `test (macos-14)` check covers darwin.
- A Linux host cross-builds linux-arm64 with aarch64-linux-gnu-gcc and windows-amd64 with x86_64-w64-mingw32-gcc-posix. Use a copy of the tree, cmake `-DCMAKE_SYSTEM_NAME` with `-DGGML_NATIVE=OFF`, and `CC`, `CXX`, `GOOS` and `GOARCH` for `go build -tags whisper`. Then run `voxthief version` under qemu-aarch64 and wine64. Only `release.yml` builds them natively and with MSYS2, after the merge to main.

### Toolchain
- Go has no release cooldown setting. Sadr dates `go.sum` entries with `lockage.py`.
- Go is pinned by the exact `go-version` input of `actions/setup-go` in `ci.yml` and `release.yml`. `go.mod` states only the language version `go 1.27`.
- whisper.cpp is a git submodule pinned to a release tag commit of ggml-org/whisper.cpp. The CI cache key for its build hashes the submodule's `CMakeLists.txt`.
