//go:build !whisper

package asr

import "errors"

// Available reports whether this binary was built with whisper support. The
// stub build (default) is false; whisper.go sets it true under `-tags whisper`.
const Available = false

// New (stub) is compiled into every build that does NOT set `-tags whisper`.
// It lets the whole module build and test without the C toolchain or the
// vendored whisper.cpp static library. The real implementation lives in
// whisper.go and is selected by the `whisper` build tag (set by `make build`).
func New(p Params) (Transcriber, error) {
	return nil, errors.New("voxthief built without whisper support; rebuild with: make build (which sets -tags whisper) after make whisper")
}
