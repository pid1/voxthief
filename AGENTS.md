# AGENTS.md

## Sadr

### Build and test
- Run `git submodule update --init --recursive` first. The whisper.cpp source lives in the `third_party/whisper.cpp` submodule.
- `make whisper` builds the static whisper.cpp libraries. It needs cmake and a C/C++ toolchain.
- `go vet ./...` and `golangci-lint run --timeout=5m ./...` pass with 0 issues. CI installs golangci-lint with `go install` at the version pinned in `.github/workflows/ci.yml`.
- `go test -race ./...` runs the unit tests without cgo or whisper.
- `make build && ./voxthief version` builds the cgo binary with whisper linked in and prints its version.
- The ASR integration tests run with `VOXTHIEF_MODELS_DIR=DIR VOXTHIEF_SPEECH_WAV=third_party/whisper.cpp/samples/jfk.wav CGO_ENABLED=1 go test -race -tags "whisper integration" ./internal/asr/...`. DIR holds `ggml-tiny.en.bin`, `ggml-small.en-q8_0.bin` and `ggml-silero-v6.2.0.bin`, from the URLs and SHA-256 sums in `internal/asr/models.go`.
- `TestVADTimelineMapping` skips because its synthetic tone yields no segments. `TestVADTimelineMappingRealSpeech` with `jfk.wav` is the check that whisper's VAD timestamps map back to the original audio.
- A Linux host cannot build the darwin-arm64 (Metal) or windows-amd64 (MSYS2) targets. The CI `test (macos-14)` check covers darwin. Only `release.yml` builds windows-amd64 and linux-arm64, after the merge to main.

### Toolchain
- Go has no release cooldown setting. Sadr dates `go.sum` entries with `lockage.py`.
- Go is pinned by the exact `go-version` input of `actions/setup-go` in `ci.yml` and `release.yml`. `go.mod` states only the language version `go 1.27`.
- whisper.cpp is a git submodule pinned to a release tag commit of ggml-org/whisper.cpp. The CI cache key for its build hashes the submodule's `CMakeLists.txt`.
