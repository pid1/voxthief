package config

import (
	"fmt"
	"os"
	"runtime"
)

// PermWarning returns a one-line warning if the config file at path holds
// secrets and is group- or world-readable on unix. Empty string means OK. The
// check is skipped on Windows (§12).
func PermWarning(path string, hasSecrets bool) (string, error) {
	if !hasSecrets || runtime.GOOS == "windows" {
		return "", nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if info.Mode().Perm()&0o077 != 0 {
		return fmt.Sprintf(
			"warning: %s holds Pushover secrets but is group/world-readable (mode %#o); run: chmod 600 %s",
			path, info.Mode().Perm(), path,
		), nil
	}
	return "", nil
}
