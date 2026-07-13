package main

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/spf13/cobra"

	"github.com/pid1/voxthief/internal/audio"
)

func newCalibrateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "calibrate",
		Short: "Sample the input and recommend a squelch threshold",
		RunE:  runCalibrate,
	}
	cmd.Flags().String("input", "", "input spec: soundcard[:SEL] | rtlsdr:FREQ")
	cmd.Flags().Int("seconds", 10, "sampling duration")
	addSDRFlags(cmd)
	return cmd
}

func runCalibrate(cmd *cobra.Command, _ []string) error {
	cfg, _, err := loadConfig(cmd)
	if err != nil {
		return err
	}
	source, err := buildSource(cmd, cfg)
	if err != nil {
		return err
	}
	seconds, _ := cmd.Flags().GetInt("seconds")

	ctx, cancel := context.WithTimeout(cmd.Context(), time.Duration(seconds)*time.Second)
	defer cancel()

	blocks, err := source.Start(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = source.Stop() }()

	fmt.Printf("sampling %s for %ds — keep the channel idle for an accurate noise floor…\n",
		source.Descriptor(), seconds)

	var rms []float64
	for {
		select {
		case b, ok := <-blocks:
			if !ok {
				return report(rms)
			}
			rms = append(rms, audio.RMSDBFS(b.Samples))
		case <-ctx.Done():
			return report(rms)
		}
	}
}

func report(rms []float64) error {
	if len(rms) == 0 {
		return fmt.Errorf("no audio captured")
	}
	sort.Float64s(rms)
	floor := percentile(rms, 0.20) // representative noise floor
	peak := rms[len(rms)-1]
	median := percentile(rms, 0.50)
	// Recommend a threshold a margin above the noise floor, bounded sanely.
	suggested := floor + 12
	if suggested > -20 {
		suggested = -20
	}
	fmt.Printf("noise floor (p20): %6.1f dBFS\n", floor)
	fmt.Printf("median:            %6.1f dBFS\n", median)
	fmt.Printf("peak:              %6.1f dBFS\n", peak)
	fmt.Printf("\nrecommended threshold_dbfs: %.1f\n", suggested)
	fmt.Println("set it under [audio] in your config, or pass --threshold to listen.")
	return nil
}

func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(p * float64(len(sorted)-1))
	return sorted[idx]
}
