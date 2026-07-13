package main

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/pid1/voxthief/internal/audio"
)

func newInputsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "inputs",
		Short: "List usable audio inputs (soundcard and RTL-SDR)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			printSoundcards()
			printRTLSDR(cmd.Context())
			return nil
		},
	}
}

func printSoundcards() {
	fmt.Println("SOUNDCARD")
	devs, err := audio.EnumerateCaptureDevices()
	if err != nil {
		fmt.Printf("  (enumeration failed: %v)\n", err)
		return
	}
	if len(devs) == 0 {
		fmt.Println("  (no capture devices found)")
		return
	}
	for _, d := range devs {
		hint := ""
		if looksLikeAdapter(d.Name) {
			hint = "   ← likely your adapter"
		}
		fmt.Printf("  %-2d %-32s %d ch%s\n", d.Index, d.Name, d.Channels, hint)
	}
}

func looksLikeAdapter(name string) bool {
	n := strings.ToLower(name)
	return strings.Contains(n, "usb") || strings.Contains(n, "pnp")
}

// printRTLSDR probes rtl_test -t with a short timeout (§4.1). If the tools are
// absent it prints an install hint rather than hiding the section.
func printRTLSDR(parent context.Context) {
	fmt.Println("RTL-SDR")
	path, err := exec.LookPath("rtl_test")
	if err != nil {
		fmt.Println("  (rtl-sdr tools not found; install rtl-sdr to enable SDR input)")
		return
	}
	ctx, cancel := context.WithTimeout(parent, 3*time.Second)
	defer cancel()
	out, _ := exec.CommandContext(ctx, "rtl_test", "-t").CombinedOutput()
	rtlfm, _ := exec.LookPath("rtl_fm")
	found := "not found"
	if rtlfm != "" {
		found = rtlfm
	}
	printed := false
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		// rtl_test lists devices as "  0:  Realtek, RTL2838UHIDIR, SN: ..."
		if len(line) > 2 && line[0] >= '0' && line[0] <= '9' && line[1] == ':' {
			fmt.Printf("  %s  (rtl_fm: %s)\n", line, found)
			printed = true
		}
	}
	if !printed {
		fmt.Printf("  (rtl_test found at %s but reported no devices)\n", path)
	}
}
