package main

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/pid1/voxthief/internal/alerts"
)

func newAlertsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "alerts",
		Short: "Alert utilities",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "test",
		Short: "Validate Pushover credentials and send a real test notification",
		RunE:  runAlertsTest,
	})
	return cmd
}

// runAlertsTest validates the credentials for a crisp error, then sends a real
// notification through the normal dispatch path (§7.3).
func runAlertsTest(cmd *cobra.Command, _ []string) error {
	cfg, _, err := loadConfig(cmd)
	if err != nil {
		return err
	}
	if cfg.Pushover.Token == "" || cfg.Pushover.UserKey == "" {
		return fmt.Errorf("[pushover] token and user_key must be set in the config file (secrets have no flags or env vars)")
	}

	client := alerts.NewClient(cfg.Pushover.Token, cfg.Pushover.UserKey)
	ctx := cmd.Context()

	fmt.Println("validating credentials…")
	if err := client.Validate(ctx); err != nil {
		return err
	}
	fmt.Println("credentials OK; sending test notification…")

	status, err := client.Send(ctx, alerts.Message{
		Message:   "voxthief test notification — alerts are configured correctly.",
		Title:     "voxthief: test",
		Timestamp: time.Now(),
	})
	if err != nil {
		return fmt.Errorf("send failed (HTTP %d): %w", status, err)
	}
	fmt.Printf("test notification sent (HTTP %d)\n", status)
	return nil
}
