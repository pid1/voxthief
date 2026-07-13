package config

import (
	"fmt"
	"os"
	"path/filepath"
)

// defaultTOML is the full commented config written by `voxthief config init`.
// Backslashes in regex patterns use single-quoted TOML literals so they pass
// through unescaped (§12).
const defaultTOML = `# voxthief configuration
# Precedence: CLI flags > this file > built-in defaults.
# Secrets ([pushover]) are read ONLY from this file. This file must be mode 0600.

[source]
type = "soundcard"          # "soundcard" | "rtlsdr"
device = "default"          # index | name substring | "default"
frequency = "146.520M"      # rtlsdr only; bare Hz or k/M/G suffix
gain = "auto"               # "auto" or dB, e.g. "28"
squelch_level = 50          # rtlsdr hardware squelch (rtl_fm -l)
ppm = 0                     # rtlsdr frequency correction
sdr_device_index = 0        # rtlsdr device index
wide_fm = false             # rtlsdr: true = wideband broadcast FM, false = narrowband (2-way)

[audio]
threshold_dbfs = -45.0      # RMS gate per 20 ms block
hang_time_s = 1.75          # time below threshold to CLOSE
preroll_ms = 400            # audio prepended on OPEN
max_segment_s = 120         # hard cap; stuck-squelch guard
min_segment_s = 0.4         # shorter segments discarded

[transcription]
model = "small.en-q8_0"
beam_size = 5
language = "en"
initial_prompt = ""
blocklist = []              # extra phrases to drop (hallucination filter)
workers = 1

[storage]
db_path = ""                # empty = <xdg-data>/voxthief/voxthief.db
retain_audio = true
audio_dir = ""              # empty = <xdg-data>/voxthief/audio
retention_days = 14

[pushover]
token = ""                  # application API token (secret; file must be 0600)
user_key = ""               # user or group key (secret)

[alerts]
enabled = false
max_per_hour = 60           # global sliding-window cap across all rules

# [[alerts.rules]]
# name = "callsign"
# pattern = '\bK5ABC\b'     # RE2; single-quoted TOML literal so backslashes pass through
# case_sensitive = false
# priority = 0              # -2..2; 2 requires retry and expire below
# sound = ""                # Pushover sound name; empty = account default
# cooldown_s = 0
# retry = 60                # required iff priority = 2
# expire = 3600            # required iff priority = 2
`

// WriteDefault writes the commented default config to path (or the default
// path when empty) with mode 0600. It refuses to overwrite an existing file.
// Returns the path written.
func WriteDefault(path string) (string, error) {
	if path == "" {
		p, err := DefaultPath()
		if err != nil {
			return "", err
		}
		path = p
	}
	if _, err := os.Stat(path); err == nil {
		return path, fmt.Errorf("refusing to overwrite existing config at %s", path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(defaultTOML), 0o600); err != nil {
		return "", err
	}
	return path, nil
}
