package main

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/pid1/voxthief/internal/export"
)

func newExportCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "export",
		Short: "Export stored transmissions",
		RunE:  runExport,
	}
	cmd.Flags().String("since", "", "start time: RFC3339 or a duration like 24h/7d (required)")
	cmd.Flags().String("until", "", "end time: RFC3339 (default: now)")
	cmd.Flags().String("format", "jsonl", "jsonl | csv | txt")
	cmd.Flags().Bool("include-filtered", false, "include filtered/error rows")
	cmd.Flags().StringP("output", "o", "", "output file (default: stdout)")
	_ = cmd.MarkFlagRequired("since")
	return cmd
}

func runExport(cmd *cobra.Command, _ []string) error {
	store, err := openStore(cmd)
	if err != nil {
		return err
	}
	defer store.Close()
	if err := store.EnsureHead(cmd.Context()); err != nil {
		return err
	}

	now := time.Now()
	sinceStr, _ := cmd.Flags().GetString("since")
	since, err := export.ParseSince(sinceStr, now)
	if err != nil {
		return err
	}
	until := now
	if u, _ := cmd.Flags().GetString("until"); u != "" {
		if until, err = export.ParseSince(u, now); err != nil {
			return err
		}
	}

	includeFiltered, _ := cmd.Flags().GetBool("include-filtered")
	rows, err := store.ListTransmissionsSince(cmd.Context(), since, until, includeFiltered)
	if err != nil {
		return err
	}

	var w io.Writer = os.Stdout
	if out, _ := cmd.Flags().GetString("output"); out != "" {
		f, err := os.Create(out)
		if err != nil {
			return err
		}
		defer f.Close()
		w = f
	}

	format, _ := cmd.Flags().GetString("format")
	if err := export.Write(w, format, rows); err != nil {
		return err
	}
	if _, ok := w.(*os.File); ok && w != os.Stdout {
		fmt.Fprintf(os.Stderr, "exported %d transmissions\n", len(rows))
	}
	return nil
}
