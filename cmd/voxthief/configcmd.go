package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/pid1/voxthief/internal/config"
)

func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Configuration file management",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "init",
		Short: "Write a commented default config (mode 0600)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, _ := cmd.Flags().GetString("config")
			written, err := config.WriteDefault(path)
			if err != nil {
				return err
			}
			fmt.Printf("wrote %s (mode 0600)\n", written)
			fmt.Println("edit [pushover] and [[alerts.rules]] to enable keyword alerts")
			return nil
		},
	})
	return cmd
}
