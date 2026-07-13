package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/pid1/voxthief/internal/db"
)

func newDBCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "db",
		Short: "Database migrations",
	}
	cmd.AddCommand(
		&cobra.Command{
			Use:   "upgrade",
			Short: "Apply pending migrations",
			RunE: func(cmd *cobra.Command, _ []string) error {
				store, err := openStore(cmd)
				if err != nil {
					return err
				}
				defer func() { _ = store.Close() }()
				v, err := store.Upgrade(cmd.Context())
				if err != nil {
					return err
				}
				fmt.Printf("database upgraded to migration %d\n", v)
				return nil
			},
		},
		&cobra.Command{
			Use:   "status",
			Short: "Show migration status",
			RunE: func(cmd *cobra.Command, _ []string) error {
				store, err := openStore(cmd)
				if err != nil {
					return err
				}
				defer func() { _ = store.Close() }()
				cur, head, err := store.Status(cmd.Context())
				if err != nil {
					return err
				}
				fmt.Printf("current: %d  head: %d\n", cur, head)
				if cur < head {
					fmt.Println("pending migrations; run: voxthief db upgrade")
				} else {
					fmt.Println("up to date")
				}
				return nil
			},
		},
	)
	return cmd
}

// openStore opens the configured database (no head check — db commands manage
// the schema themselves).
func openStore(cmd *cobra.Command) (*db.Store, error) {
	cfg, _, err := loadConfig(cmd)
	if err != nil {
		return nil, err
	}
	path, err := cfg.ResolveDBPath()
	if err != nil {
		return nil, err
	}
	return db.Open(path)
}
