package audio

import "math"

// dbfsFloor is returned in place of -Inf for digital silence so the meter and
// RMS gate stay on a finite scale (§5).
const dbfsFloor = -120.0

// RMSDBFS returns the root-mean-square level of samples in dBFS. dBFS is
// 20*log10(rms) with 0 dBFS at full scale (±1.0). Empty or silent input
// returns dbfsFloor rather than -Inf.
func RMSDBFS(samples []float32) float64 {
	if len(samples) == 0 {
		return dbfsFloor
	}
	var sumSq float64
	for _, s := range samples {
		v := float64(s)
		sumSq += v * v
	}
	rms := math.Sqrt(sumSq / float64(len(samples)))
	if rms <= 0 {
		return dbfsFloor
	}
	return 20 * math.Log10(rms)
}

// PeakDBFS returns the peak absolute sample level of samples in dBFS. Empty or
// silent input returns dbfsFloor rather than -Inf.
func PeakDBFS(samples []float32) float64 {
	var peak float64
	for _, s := range samples {
		v := math.Abs(float64(s))
		if v > peak {
			peak = v
		}
	}
	if peak <= 0 {
		return dbfsFloor
	}
	return 20 * math.Log10(peak)
}
