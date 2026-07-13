//go:build whisper

// The §6 correctness landmine (§16.3): with VAD enabled, whisper trims leading
// silence internally, but the returned segment timestamps MUST map back onto
// the original, untrimmed timeline. Builds only with `-tags whisper`; runs when
// VOXTHIEF_MODELS_DIR and VOXTHIEF_SPEECH_WAV are set.
package asr

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

func TestVADTimelineMappingRealSpeech(t *testing.T) {
	modelsDir := os.Getenv("VOXTHIEF_MODELS_DIR")
	wav := os.Getenv("VOXTHIEF_SPEECH_WAV")
	if modelsDir == "" || wav == "" {
		t.Skip("set VOXTHIEF_MODELS_DIR and VOXTHIEF_SPEECH_WAV to run the VAD timeline test")
	}
	speech, err := readWAV16kMono(wav)
	if err != nil {
		t.Fatal(err)
	}

	const leadSeconds = 3
	buf := make([]float32, leadSeconds*16000) // leading silence
	buf = append(buf, speech...)              // then the real speech

	tr, err := New(Params{
		ModelPath:    filepath.Join(modelsDir, "ggml-small.en-q8_0.bin"),
		VADModelPath: filepath.Join(modelsDir, VADModelFilename),
		BeamSize:     5, Language: "en", NoContext: true, Threads: DefaultThreads(), VAD: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()

	res, err := tr.Transcribe(buf)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Segments) == 0 {
		t.Fatal("expected speech segments")
	}
	first := res.Segments[0]
	t.Logf("first segment: start=%.2fs end=%.2fs text=%q", first.Start, first.End, first.Text)
	// If VAD-trimmed offsets leaked through, start would be near 0. The speech
	// begins at leadSeconds, so the mapped start must be well past it.
	if first.Start < float64(leadSeconds)-0.75 {
		t.Fatalf("segment start %.2fs maps to the trimmed timeline; expected ~%ds (original)", first.Start, leadSeconds)
	}
}

// readWAV16kMono decodes a canonical 16 kHz mono 16-bit PCM WAV to float32.
func readWAV16kMono(path string) ([]float32, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	// Find the "data" chunk.
	i := 12
	for i+8 <= len(data) {
		id := string(data[i : i+4])
		size := int(binary.LittleEndian.Uint32(data[i+4 : i+8]))
		body := i + 8
		if id == "data" {
			end := body + size
			if end > len(data) {
				end = len(data)
			}
			pcm := data[body:end]
			out := make([]float32, len(pcm)/2)
			for j := range out {
				out[j] = float32(int16(binary.LittleEndian.Uint16(pcm[j*2:]))) / 32768
			}
			return out, nil
		}
		i = body + size + (size & 1)
	}
	return nil, os.ErrInvalid
}
