package audio

import (
	"math"
	"testing"
)

// sine returns n samples of a full-amplitude sine at freqHz for a 16 kHz rate.
func sine(n int, freqHz float64, amp float32) []float32 {
	out := make([]float32, n)
	for i := range out {
		out[i] = amp * float32(math.Sin(2*math.Pi*freqHz*float64(i)/float64(SampleRate)))
	}
	return out
}

func TestRMSDBFS(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		samples []float32
		want    float64
		tol     float64
	}{
		{"silence", make([]float32, 320), dbfsFloor, 0},
		{"empty", nil, dbfsFloor, 0},
		// Full-scale sine has RMS = 1/sqrt(2) ≈ 0.707 → ~-3.01 dBFS.
		{"full_scale_sine", sine(16000, 1000, 1.0), -3.0103, 0.05},
		// Half-amplitude sine is 6.02 dB lower.
		{"half_sine", sine(16000, 1000, 0.5), -9.0309, 0.05},
		// DC full scale: rms = 1 → 0 dBFS.
		{"dc_full", []float32{1, 1, 1, 1}, 0, 1e-9},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := RMSDBFS(tt.samples)
			if math.Abs(got-tt.want) > tt.tol {
				t.Fatalf("RMSDBFS = %v, want %v (±%v)", got, tt.want, tt.tol)
			}
		})
	}
}

func TestPeakDBFS(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		samples []float32
		want    float64
		tol     float64
	}{
		{"silence", make([]float32, 320), dbfsFloor, 0},
		{"empty", nil, dbfsFloor, 0},
		{"full_scale", []float32{0, 1.0, -0.5, 0.25}, 0, 1e-9},
		{"half_scale", []float32{0, 0.5, -0.25}, -6.0206, 0.01},
		// Peak of a full-scale sine reaches ~1.0 → ~0 dBFS.
		{"full_sine_peak", sine(16000, 1000, 1.0), 0, 0.1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := PeakDBFS(tt.samples)
			if math.Abs(got-tt.want) > tt.tol {
				t.Fatalf("PeakDBFS = %v, want %v (±%v)", got, tt.want, tt.tol)
			}
		})
	}
}
