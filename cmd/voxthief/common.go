package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/pid1/voxthief/internal/audio"
	"github.com/pid1/voxthief/internal/config"
)

// loadConfig loads the config file named by --config (or the default path),
// validates it, and prints the secret-permission warning when applicable (§12).
func loadConfig(cmd *cobra.Command) (config.Config, string, error) {
	path, _ := cmd.Flags().GetString("config")
	cfg, _, err := config.Load(path)
	if err != nil {
		return cfg, path, err
	}
	if err := cfg.Validate(); err != nil {
		return cfg, path, err
	}
	// Resolve the actual path for the perm warning.
	if path == "" {
		if p, perr := config.DefaultPath(); perr == nil {
			path = p
		}
	}
	if path != "" {
		if warn, _ := config.PermWarning(path, cfg.HasSecrets()); warn != "" {
			fmt.Fprintln(os.Stderr, warn)
		}
	}
	return cfg, path, nil
}

// sdrFlags holds the SDR-only flags, valid only with --input rtlsdr:* (§4.1).
type sdrFlags struct {
	gain      string
	squelch   int
	ppm       int
	sdrDevice int
	wideFM    bool
	set       map[string]bool // which SDR flags the user explicitly set
}

// buildSource resolves the effective input spec (flags > config > default) and
// constructs the AudioSource. It enforces that SDR-only flags are used only
// with an rtlsdr input (§4.1).
func buildSource(cmd *cobra.Command, cfg config.Config) (audio.AudioSource, error) {
	input, _ := cmd.Flags().GetString("input")
	if input == "" {
		input = configInput(cfg)
	}

	kind, selector, err := splitInput(input)
	if err != nil {
		return nil, err
	}

	sf := readSDRFlags(cmd)
	if kind != "rtlsdr" && len(sf.set) > 0 {
		return nil, fmt.Errorf("--%s is valid only with --input rtlsdr:*", anyKey(sf.set))
	}

	switch kind {
	case "soundcard":
		if selector == "" {
			selector = cfg.Source.Device
		}
		return &audio.SoundCardSource{Selector: selector}, nil
	case "rtlsdr":
		if selector == "" {
			selector = cfg.Source.Frequency
		}
		freq, err := audio.ParseFrequency(selector)
		if err != nil {
			return nil, err
		}
		gain := cfg.Source.Gain
		if sf.set["gain"] {
			gain = sf.gain
		}
		squelch := cfg.Source.SquelchLevel
		if sf.set["squelch"] {
			squelch = sf.squelch
		}
		ppm := cfg.Source.PPM
		if sf.set["ppm"] {
			ppm = sf.ppm
		}
		dev := cfg.Source.SDRDeviceIndex
		if sf.set["sdr-device"] {
			dev = sf.sdrDevice
		}
		src := audio.NewRTLSDRSource(freq, dev, gain, ppm, squelch)
		src.WideFM = cfg.Source.WideFM
		if sf.set["wfm"] {
			src.WideFM = sf.wideFM
		}
		return src, nil
	default:
		return nil, fmt.Errorf("unknown input kind %q (want soundcard or rtlsdr)", kind)
	}
}

// configInput reconstructs an input spec from the config source section.
func configInput(cfg config.Config) string {
	switch cfg.Source.Type {
	case "rtlsdr":
		return "rtlsdr:" + cfg.Source.Frequency
	default:
		dev := cfg.Source.Device
		if dev == "" {
			dev = "default"
		}
		return "soundcard:" + dev
	}
}

// splitInput parses "kind[:selector]" (§4.1).
func splitInput(spec string) (kind, selector string, err error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return "", "", fmt.Errorf("empty --input")
	}
	if i := strings.IndexByte(spec, ':'); i >= 0 {
		return spec[:i], spec[i+1:], nil
	}
	return spec, "", nil
}

func readSDRFlags(cmd *cobra.Command) sdrFlags {
	sf := sdrFlags{set: map[string]bool{}}
	sf.gain, _ = cmd.Flags().GetString("gain")
	sf.squelch, _ = cmd.Flags().GetInt("squelch")
	sf.ppm, _ = cmd.Flags().GetInt("ppm")
	sf.sdrDevice, _ = cmd.Flags().GetInt("sdr-device")
	sf.wideFM, _ = cmd.Flags().GetBool("wfm")
	for _, name := range []string{"gain", "squelch", "ppm", "sdr-device", "wfm"} {
		if f := cmd.Flags().Lookup(name); f != nil && f.Changed {
			sf.set[name] = true
		}
	}
	return sf
}

func anyKey(m map[string]bool) string {
	for k := range m {
		return k
	}
	return ""
}

// addSDRFlags registers the SDR-only flags on a command.
func addSDRFlags(cmd *cobra.Command) {
	cmd.Flags().String("gain", "auto", "SDR gain in dB, or 'auto' (rtlsdr only)")
	cmd.Flags().Int("squelch", 50, "SDR hardware squelch level (rtlsdr only)")
	cmd.Flags().Int("ppm", 0, "SDR frequency correction in ppm (rtlsdr only)")
	cmd.Flags().Int("sdr-device", 0, "SDR device index (rtlsdr only)")
	cmd.Flags().Bool("wfm", false, "wideband (broadcast) FM instead of narrowband (rtlsdr only)")
}

// atoiDefault parses s or returns def.
func atoiDefault(s string, def int) int {
	if n, err := strconv.Atoi(s); err == nil {
		return n
	}
	return def
}
