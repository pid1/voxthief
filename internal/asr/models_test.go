package asr

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/pid1/voxthief/internal/events"
)

func TestModelFilename(t *testing.T) {
	cases := map[string]string{
		"small.en-q8_0":          "ggml-small.en-q8_0.bin",
		"tiny.en":                "ggml-tiny.en.bin",
		"ggml-small.en-q8_0.bin": "ggml-small.en-q8_0.bin",
		"custom.bin":             "custom.bin",
	}
	for in, want := range cases {
		if got := ModelFilename(in); got != want {
			t.Errorf("ModelFilename(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestEnsureModelExistingFileNoDownload(t *testing.T) {
	dir := t.TempDir()
	name := "ggml-tiny.en.bin"
	if err := os.WriteFile(filepath.Join(dir, name), []byte("already here"), 0o644); err != nil {
		t.Fatal(err)
	}
	// No server; if it tried to download it would fail.
	path, err := EnsureModel(context.Background(), name, dir, nil)
	if err != nil {
		t.Fatalf("EnsureModel: %v", err)
	}
	if path != filepath.Join(dir, name) {
		t.Errorf("path = %q", path)
	}
}

func TestEnsureModelDownloadSkipVerify(t *testing.T) {
	// A payload large enough to stream in several chunks so intermediate
	// progress fractions fire.
	payload := make([]byte, 40*1024)
	for i := range payload {
		payload[i] = byte(i)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		for off := 0; off < len(payload); off += 8 * 1024 {
			end := min(off+8*1024, len(payload))
			_, _ = w.Write(payload[off:end])
			if flusher != nil {
				flusher.Flush()
			}
		}
	}))
	defer srv.Close()

	old := testBaseOverride
	testBaseOverride = srv.URL
	defer func() { testBaseOverride = old }()

	// This test covers the empty-pin skip path: clear the pin for the duration
	// so the fake payload is accepted without checksum verification.
	oldEntry := registry["ggml-tiny.en.bin"]
	registry["ggml-tiny.en.bin"] = modelEntry{URL: oldEntry.URL, SHA256: ""}
	defer func() { registry["ggml-tiny.en.bin"] = oldEntry }()

	dir := t.TempDir()
	var sawProgress, sawDone bool
	progress := func(m events.ModelProgressMsg) {
		if m.Fraction > 0 && m.Fraction < 1 {
			sawProgress = true
		}
		if m.Done {
			sawDone = true
		}
	}
	// Empty pin → verification skipped (no error).
	path, err := EnsureModel(context.Background(), "ggml-tiny.en.bin", dir, progress)
	if err != nil {
		t.Fatalf("EnsureModel: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(payload) {
		t.Errorf("downloaded content mismatch")
	}
	if !sawProgress {
		t.Errorf("expected intermediate progress callbacks")
	}
	if !sawDone {
		t.Errorf("expected a Done progress callback")
	}
}

// TestEnsureModelRealDownloadVerifies exercises the real Hugging Face download
// and confirms it passes the pinned-SHA256 verification. Network-gated so
// normal/CI runs skip it; run with VOXTHIEF_NET_TEST=1.
func TestEnsureModelRealDownloadVerifies(t *testing.T) {
	if os.Getenv("VOXTHIEF_NET_TEST") == "" {
		t.Skip("set VOXTHIEF_NET_TEST=1 to hit the network and verify pinned checksums")
	}
	dir := t.TempDir()
	// The Silero VAD model is small (~0.9 MB) and pinned.
	path, err := EnsureModel(context.Background(), VADModelFilename, dir, nil)
	if err != nil {
		t.Fatalf("EnsureModel with pinned SHA failed verification: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("model not present: %v", err)
	}
}

func TestDownloadChecksumMismatch(t *testing.T) {
	payload := []byte("bytes")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	tmp := filepath.Join(t.TempDir(), "m.part")
	// Wrong pinned hash → error and no file left behind by EnsureModel's cleanup.
	wrong := hex.EncodeToString(sha256.New().Sum(nil))
	err := download(context.Background(), srv.URL, tmp, wrong, "m", func(events.ModelProgressMsg) {})
	if err == nil {
		t.Fatal("expected checksum mismatch error")
	}
}
