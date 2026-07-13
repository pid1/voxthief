package asr

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/pid1/voxthief/internal/events"
)

// modelEntry describes a downloadable GGML model. SHA256 is the pinned checksum
// of the resolved file; an empty pin skips verification with a logged warning.
type modelEntry struct {
	URL    string
	SHA256 string
}

// baseURL is overridable in tests to point at an httptest server. In production
// it is empty and entry URLs are absolute Hugging Face resolve links.
var testBaseOverride string

// registry maps GGML filename → source. Whisper models come from the official
// whisper.cpp repo; the VAD model from ggml-org/whisper-vad (§6).
var registry = map[string]modelEntry{
	"ggml-small.en-q8_0.bin": {
		URL:    "https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-small.en-q8_0.bin",
		SHA256: "67a179f608ea6114bd3fdb9060e762b588a3fb3bd00c4387971be4d177958067",
	},
	"ggml-tiny.en.bin": {
		URL:    "https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-tiny.en.bin",
		SHA256: "921e4cf8686fdd993dcd081a5da5b6c365bfde1162e72b08d75ac75289920b1f",
	},
	"ggml-silero-v6.2.0.bin": {
		URL:    "https://huggingface.co/ggml-org/whisper-vad/resolve/main/ggml-silero-v6.2.0.bin",
		SHA256: "2aa269b785eeb53a82983a20501ddf7c1d9c48e33ab63a41391ac6c9f7fb6987",
	},
}

// ModelFilename maps a config model name to its GGML filename. A value that is
// already a filename passes through. E.g. "small.en-q8_0" →
// "ggml-small.en-q8_0.bin".
func ModelFilename(name string) string {
	if strings.HasPrefix(name, "ggml-") || strings.HasSuffix(name, ".bin") {
		return name
	}
	return "ggml-" + name + ".bin"
}

// VADModelFilename is the fixed Silero VAD model (§2.3, §6).
const VADModelFilename = "ggml-silero-v6.2.0.bin"

// EnsureModel returns the local path to the named GGML model, downloading it on
// first run with streaming progress (§6). If the file already exists it is
// returned as-is. When the registry pins a SHA256 the download is verified;
// an empty pin skips verification with a logged warning.
func EnsureModel(ctx context.Context, name, dir string, progress func(events.ModelProgressMsg)) (string, error) {
	entry, ok := registry[name]
	if !ok {
		return "", fmt.Errorf("unknown model %q", name)
	}
	dest := filepath.Join(dir, name)
	if _, err := os.Stat(dest); err == nil {
		return dest, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}

	url := entry.URL
	if testBaseOverride != "" {
		url = testBaseOverride + "/" + name
	}

	notify := func(m events.ModelProgressMsg) {
		if progress != nil {
			progress(m)
		}
	}
	notify(events.ModelProgressMsg{Name: name})

	tmp := dest + ".part"
	if err := download(ctx, url, tmp, entry.SHA256, name, notify); err != nil {
		_ = os.Remove(tmp)
		notify(events.ModelProgressMsg{Name: name, Err: err.Error()})
		return "", err
	}
	if err := os.Rename(tmp, dest); err != nil {
		return "", err
	}
	notify(events.ModelProgressMsg{Name: name, Fraction: 1, Done: true})
	return dest, nil
}

func download(ctx context.Context, url, tmp, wantSHA, name string, notify func(events.ModelProgressMsg)) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: HTTP %d", name, resp.StatusCode)
	}

	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	defer f.Close()

	h := sha256.New()
	total := resp.ContentLength
	pw := &progressWriter{total: total, name: name, notify: notify}
	if _, err := io.Copy(io.MultiWriter(f, h, pw), resp.Body); err != nil {
		return err
	}

	if wantSHA == "" {
		slog.Warn("model downloaded without pinned checksum; verification skipped",
			"model", name)
		return nil
	}
	got := hex.EncodeToString(h.Sum(nil))
	if !strings.EqualFold(got, wantSHA) {
		return fmt.Errorf("checksum mismatch for %s: got %s want %s", name, got, wantSHA)
	}
	return nil
}

// progressWriter reports download fraction from Content-Length (§6).
type progressWriter struct {
	total   int64
	written int64
	name    string
	notify  func(events.ModelProgressMsg)
	lastPct int
}

func (p *progressWriter) Write(b []byte) (int, error) {
	n := len(b)
	p.written += int64(n)
	if p.total > 0 {
		frac := float64(p.written) / float64(p.total)
		if pct := int(frac * 100); pct != p.lastPct {
			p.lastPct = pct
			p.notify(events.ModelProgressMsg{Name: p.name, Fraction: frac})
		}
	}
	return n, nil
}
