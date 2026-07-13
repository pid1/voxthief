package audio

import (
	"math"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestWriteWAVRoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "rt.wav")

	in := []float32{0, 0.5, -0.5, 1, -1, 0.25, -0.75, 0.123}
	if err := WriteWAV(path, in); err != nil {
		t.Fatalf("WriteWAV: %v", err)
	}
	got, err := decodeWAVMono16k(path)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != len(in) {
		t.Fatalf("len = %d, want %d", len(got), len(in))
	}
	for i := range in {
		if math.Abs(float64(got[i]-in[i])) > 1e-4 {
			t.Errorf("sample %d = %v, want ~%v", i, got[i], in[i])
		}
	}
}

func TestWriteWAVClamps(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "clamp.wav")

	in := []float32{2.0, -2.0} // out of range, must clamp to ±full-scale
	if err := WriteWAV(path, in); err != nil {
		t.Fatalf("WriteWAV: %v", err)
	}
	got, err := decodeWAVMono16k(path)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got[0] < 0.99 || got[1] > -0.99 {
		t.Errorf("clamp failed: got %v", got)
	}
}

func TestAudioPath(t *testing.T) {
	t.Parallel()
	// 2026-07-12 13:04:05.678 UTC
	ts := time.Date(2026, 7, 12, 13, 4, 5, 678_000_000, time.UTC)
	got := AudioPath("/data", ts)
	exp := filepath.Join("/data", "2026", "07", "12",
		strconv.FormatInt(ts.UnixMilli(), 10)+".wav")
	if got != exp {
		t.Errorf("AudioPath = %q, want %q", got, exp)
	}
	// Non-UTC input is normalized to UTC before formatting.
	loc := time.FixedZone("X", 3600)
	got2 := AudioPath("/data", ts.In(loc))
	if got2 != exp {
		t.Errorf("AudioPath (non-UTC) = %q, want %q", got2, exp)
	}
}

func TestPrune(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	now := time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC)

	old := AudioPath(dir, now.AddDate(0, 0, -20)) // older than retention
	recent := AudioPath(dir, now.AddDate(0, 0, -1))
	for _, p := range []string{old, recent} {
		if err := WriteWAV(p, []float32{0, 0.1}); err != nil {
			t.Fatalf("WriteWAV %s: %v", p, err)
		}
	}

	n, err := Prune(dir, 14, now)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if n != 1 {
		t.Errorf("pruned %d, want 1", n)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Errorf("old file still present")
	}
	if _, err := os.Stat(recent); err != nil {
		t.Errorf("recent file removed: %v", err)
	}
}

func TestPruneModTimeFallback(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	now := time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC)

	// A non-epoch filename falls back to modtime.
	p := filepath.Join(dir, "notes.wav")
	if err := WriteWAV(p, []float32{0}); err != nil {
		t.Fatalf("WriteWAV: %v", err)
	}
	oldTime := now.AddDate(0, 0, -30)
	if err := os.Chtimes(p, oldTime, oldTime); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}

	n, err := Prune(dir, 14, now)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if n != 1 {
		t.Errorf("pruned %d, want 1", n)
	}
}

func TestPruneMissingDir(t *testing.T) {
	t.Parallel()
	n, err := Prune(filepath.Join(t.TempDir(), "nope"), 14, time.Now())
	if err != nil {
		t.Fatalf("Prune missing dir: %v", err)
	}
	if n != 0 {
		t.Errorf("pruned %d, want 0", n)
	}
}
