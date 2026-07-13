package audio

import (
	"encoding/binary"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Audio is persisted as 16-bit PCM mono 16 kHz WAV at
// <data-dir>/audio/YYYY/MM/DD/<epoch_ms>.wav, pruned per retention_days (§5).

// WriteWAV writes samples as a 16-bit PCM mono 16 kHz WAV file at path,
// creating parent directories as needed. Samples are clamped to [-1, 1] and
// scaled to int16.
func WriteWAV(path string, samples []float32) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	const (
		numChannels   = 1
		bitsPerSample = 16
		byteRate      = SampleRate * numChannels * bitsPerSample / 8
		blockAlign    = numChannels * bitsPerSample / 8
	)
	dataLen := len(samples) * blockAlign
	riffLen := 36 + dataLen

	// Canonical 44-byte WAV/RIFF header.
	hdr := make([]byte, 0, 44)
	hdr = append(hdr, "RIFF"...)
	hdr = binary.LittleEndian.AppendUint32(hdr, uint32(riffLen))
	hdr = append(hdr, "WAVE"...)
	hdr = append(hdr, "fmt "...)
	hdr = binary.LittleEndian.AppendUint32(hdr, 16) // PCM fmt chunk size
	hdr = binary.LittleEndian.AppendUint16(hdr, 1)  // audio format PCM
	hdr = binary.LittleEndian.AppendUint16(hdr, numChannels)
	hdr = binary.LittleEndian.AppendUint32(hdr, SampleRate)
	hdr = binary.LittleEndian.AppendUint32(hdr, byteRate)
	hdr = binary.LittleEndian.AppendUint16(hdr, blockAlign)
	hdr = binary.LittleEndian.AppendUint16(hdr, bitsPerSample)
	hdr = append(hdr, "data"...)
	hdr = binary.LittleEndian.AppendUint32(hdr, uint32(dataLen))
	if _, err := f.Write(hdr); err != nil {
		return err
	}

	buf := make([]byte, dataLen)
	for i, s := range samples {
		binary.LittleEndian.PutUint16(buf[i*2:], uint16(int16(scaleSample(s))))
	}
	if _, err := f.Write(buf); err != nil {
		return err
	}
	return nil
}

// scaleSample clamps a float32 sample to [-1, 1] and scales it to the int16
// range with round-to-nearest.
func scaleSample(s float32) int32 {
	v := float64(s)
	if v > 1 {
		v = 1
	} else if v < -1 {
		v = -1
	}
	n := math.Round(v * 32767)
	if n > 32767 {
		n = 32767
	} else if n < -32768 {
		n = -32768
	}
	return int32(n)
}

// AudioPath returns <dir>/YYYY/MM/DD/<epoch_ms>.wav using the UTC date of
// startedAt (§5).
func AudioPath(dir string, startedAt time.Time) string {
	t := startedAt.UTC()
	ms := t.UnixMilli()
	return filepath.Join(dir,
		fmt.Sprintf("%04d", t.Year()),
		fmt.Sprintf("%02d", int(t.Month())),
		fmt.Sprintf("%02d", t.Day()),
		fmt.Sprintf("%d.wav", ms),
	)
}

// Prune deletes WAV files under dir whose recording time is older than
// retentionDays before now, returning the count removed (§5). A file's time is
// taken from its <epoch_ms>.wav name, falling back to modtime when the name is
// not a bare epoch. retentionDays <= 0 disables pruning.
func Prune(dir string, retentionDays int, now time.Time) (int, error) {
	if retentionDays <= 0 {
		return 0, nil
	}
	cutoff := now.Add(-time.Duration(retentionDays) * 24 * time.Hour)

	count := 0
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil // audio dir may not exist yet
			}
			return err
		}
		if d.IsDir() || !strings.EqualFold(filepath.Ext(path), ".wav") {
			return nil
		}
		ts, ok := epochFromName(path)
		if !ok {
			info, ierr := d.Info()
			if ierr != nil {
				return nil
			}
			ts = info.ModTime()
		}
		if ts.Before(cutoff) {
			if rerr := os.Remove(path); rerr != nil && !os.IsNotExist(rerr) {
				return rerr
			}
			count++
		}
		return nil
	})
	if err != nil && os.IsNotExist(err) {
		return count, nil
	}
	return count, err
}

// epochFromName parses the epoch-milliseconds timestamp encoded in a
// <epoch_ms>.wav filename, per AudioPath.
func epochFromName(path string) (time.Time, bool) {
	base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	ms, err := strconv.ParseInt(base, 10, 64)
	if err != nil {
		return time.Time{}, false
	}
	return time.UnixMilli(ms).UTC(), true
}
