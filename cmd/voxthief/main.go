// Command voxthief is the CLI entry point (§11). Subcommands: listen, inputs,
// calibrate, alerts, export, db, config, version.
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// Build metadata, injected via -ldflags "-X main.version=… -X main.commit=…".
var (
	version = "dev"
	commit  = ""
)

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "voxthief",
		Short:         "Listen to radio audio, transcribe locally, alert on keywords",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().String("config", "", "path to config.toml (default: XDG config dir)")

	root.AddCommand(
		newListenCmd(),
		newInputsCmd(),
		newCalibrateCmd(),
		newAlertsCmd(),
		newExportCmd(),
		newDBCmd(),
		newConfigCmd(),
		newVersionCmd(),
	)
	return root
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version",
		RunE: func(cmd *cobra.Command, args []string) error {
			if commit != "" {
				fmt.Printf("voxthief %s (%s)\n", version, commit)
			} else {
				fmt.Printf("voxthief %s\n", version)
			}
			return nil
		},
	}
}
