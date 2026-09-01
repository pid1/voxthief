<p align="center">
  <img src="logo.png" alt="voxthief" width="100%">
</p>

# voxthief

Hardware-agnostic ham/public-safety radio auto-transcription. voxthief listens
to radio audio, detects transmissions (squelch breaks), transcribes them locally
with [whisper.cpp](https://github.com/ggerganov/whisper.cpp), stores timestamped
transcripts in SQLite, and can push keyword-matched transmissions to your phone
via [Pushover](https://pushover.net). The primary interface is a polished
terminal UI; a headless mode exists for SSH/daemon use (a Raspberry Pi running
`voxthief listen --headless` with alert rules is the killer setup).

Everything runs offline after the first-run model download. The only outbound
network call is HTTPS to `api.pushover.net`, and only when you enable alerts.
No telemetry.

## Demo

Live NOAA weather radio transcribed off an RTL-SDR dongle, whisper running
locally:

<p align="center">
  <img src="demo.png" alt="voxthief transcribing NOAA weather radio in the terminal UI" width="100%">
</p>

## Quickstart — is it working? (RTL-SDR, ~60 seconds)

No compiler needed — grab a prebuilt binary and point it at a **strong local
broadcast FM station**. Broadcast FM is high-power and always transmitting, so
it's the fastest way to confirm your dongle, drivers, and the whole pipeline
work without waiting for anyone to key up.

**1. Download the binary** for your platform from the
[latest release](https://github.com/pid1/voxthief/releases/latest) and put it on
your `PATH`:

| Platform | Asset |
|---|---|
| macOS (Apple Silicon) | `voxthief_*_darwin_arm64.tar.gz` |
| Linux x86-64 | `voxthief_*_linux_amd64.tar.gz` |
| Linux ARM64 (Raspberry Pi 4/5) | `voxthief_*_linux_arm64.tar.gz` |
| Windows x86-64 | `voxthief_*_windows_amd64.zip` |

```
tar -xzf voxthief_*_*.tar.gz          # unzip on Windows
sudo mv voxthief /usr/local/bin/      # or anywhere on your PATH
```

On macOS the binary is unsigned, so clear the quarantine flag once:
`xattr -d com.apple.quarantine ./voxthief`.

**2. Install the RTL-SDR runtime tools** (only needed for the SDR input):

```
brew install rtl-sdr        # macOS
sudo apt install rtl-sdr    # Debian/Ubuntu/Raspberry Pi OS
```

**3. Plug in the dongle and run it** against a real local FM frequency:

```
voxthief inputs                              # confirm the dongle is listed
voxthief listen --input rtlsdr:105.3M --wfm  # use a strong local station
```

**What success looks like:** the level meter moves off the floor, the status bar
shows `SQUELCH OPEN`, and within a few seconds transcribed lines start appearing.
Press `q` to quit (it drains and transcribes the final clip on the way out).

Notes:
- **`--wfm`** selects wideband (broadcast) FM. Without it, voxthief uses
  narrowband FM for 2-way radio and broadcast stations will sound garbled.
- Audio is resampled to 16 kHz, so music sounds dull and won't transcribe well —
  pick a **talk/news** station for a clean readout. Either way, audio + a moving
  meter proves your RF path works.
- Nothing shows up? Try a different, stronger station, check the antenna, or run
  `voxthief calibrate --input rtlsdr:105.3M --wfm` to see the signal level.

The first run downloads the whisper and Silero-VAD models into your data
directory (progress shown in the UI); everything after that is offline. Once the
smoke test works, move on to the real use cases below — NOAA weather
(`--input rtlsdr:162.550M`, narrowband) or a radio into your sound card.

## Meet the user at whatever hardware they have

All three inputs are first-class and fully supported behind one `--input` flag —
everything downstream is identical and has no knowledge of which source is
running:

- A Baofeng into a **$6 USB audio adapter**
- A Baofeng into the PC's **native 3.5mm port**
- An **RTL-SDR dongle** with no radio at all

## Requirements

The prebuilt binaries are self-contained (whisper.cpp is statically linked) —
the only runtime dependency is the **rtl-sdr command-line tools**, and only if
you use the RTL-SDR input:

```
brew install rtl-sdr        # macOS
sudo apt install rtl-sdr    # Debian/Ubuntu/Raspberry Pi OS
```

Models download themselves on first run. To build from source instead of using a
release binary, see [Development](#development).

## First run

```
voxthief config init                       # writes a commented config (mode 0600)
voxthief inputs                            # list every usable input
voxthief listen --input soundcard:default  # start listening
```

## End-user setup

### A) Baofeng → 3.5mm→USB audio adapter (recommended cheap path, ~$6-10)

Mono 3.5mm cable from the radio's speaker jack (the larger pin on the Kenwood
connector) into the adapter's mic port. Radio volume 25-30% to avoid clipping.
Disable AGC if the OS exposes it.

```
voxthief inputs
voxthief listen --input soundcard:"USB PnP Sound Device"
```

### B) Baofeng → native 3.5mm port (zero-cost path)

Desktop: prefer line-in. Laptop: combo jacks are TRRS and need a TRRS headset
splitter — a plain TRS cable alone will not register.

```
voxthief listen --input soundcard:default
voxthief listen --input soundcard:1
```

### C) RTL-SDR dongle (no radio in the loop)

Requires the rtl-sdr command-line tools and an antenna for the target band.

```
voxthief listen --input rtlsdr:146.520M
voxthief listen --input rtlsdr:462.7125M --gain 28 --squelch 60 --ppm 1
voxthief listen --input rtlsdr:162.550M --sdr-device 1 --headless
```

> **Mic input tips.** An energy gate hates AGC — disable it and prefer line-in.
> Radio speaker output can clip a mic input: keep volume at 25-30%, or use a
> resistor divider. The TUI shows a `CLIP` badge when the peak is too hot.
> `voxthief calibrate` samples the input and recommends a `threshold_dbfs`.

## Configuration

Config lives at `<xdg-config>/voxthief/config.toml` (e.g.
`~/.config/voxthief/config.toml`). Precedence is **CLI flags > config file >
defaults**. `voxthief config init` writes the full commented file with mode
`0600`.

`initial_prompt` under `[transcription]` biases whisper toward your domain
vocabulary (unit numbers, agency names, local street names) — a short,
comma-separated hint noticeably improves accuracy on radio jargon.

## Keyword alerts (Pushover)

Alerts page your phone when your callsign, street, or agency shows up on the
air. Rules are Go [RE2](https://github.com/google/re2/wiki/Syntax) regexes —
plain keywords are valid regexes, so casual users can just write words; use
`\b` for word boundaries.

**Secrets live only in the config file** — deliberately no CLI flags (shell
history / process-list leakage) and no environment variables. Keep the file at
mode `0600`; voxthief warns you with the exact `chmod` fix if it isn't.

Setup walkthrough:

1. Create a [Pushover](https://pushover.net) account; note your **user key**.
2. Register an application to get an **API token**.
3. Put both in `[pushover]` and add rules:

```toml
[pushover]
token = "your-app-token"
user_key = "your-user-key"

[alerts]
enabled = true
max_per_hour = 60           # global cap protecting your monthly quota

[[alerts.rules]]
name = "callsign"
pattern = '\bK5ABC\b'       # single-quoted TOML literal so backslashes pass through
case_sensitive = false
priority = 0                # -2..2; priority 2 also requires retry and expire
sound = ""                  # Pushover sound name; empty = account default
cooldown_s = 0              # suppress repeat fires of this rule within the window
```

4. Verify end to end:

```
voxthief alerts test        # validates credentials, then sends a real push
```

One notification is sent per transmission regardless of how many rules match;
its title lists the matched rules and its priority is the maximum across them.
Filtered (VAD/hallucination) rows never alert. A misfiring `.*`-style rule
exhausts the hourly cap, not your monthly quota.

## Headless / daemon

```
voxthief listen --headless
```

Runs the identical pipeline — including alert dispatch — with no TUI, emitting
one JSON object per finished transmission on stdout (`started_at`, `duration_s`,
`source`, `frequency_hz`, `text`, `status`, `avg_logprob`, `alerted`,
`alert_rules`). This is the Raspberry Pi path; it drains cleanly on
SIGINT/SIGTERM.

## Export

```
voxthief export --since 24h --format jsonl
voxthief export --since 2026-07-01 --until 2026-07-08 --format csv -o week.csv
voxthief export --since 6h --include-filtered --format txt
```

## Commands

```
voxthief listen [--input SPEC] [--headless] [--db PATH] [--model NAME]
                [--threshold DBFS] [--gain auto|DB] [--squelch N] [--ppm N]
                [--sdr-device N] [--config PATH] [--verbose]
voxthief inputs
voxthief calibrate [--input SPEC] [--seconds 10]
voxthief alerts test
voxthief export --since ISO|"24h" [--until …] [--format jsonl|csv|txt]
                [--include-filtered] [-o FILE]
voxthief db upgrade | status
voxthief config init
voxthief version
```

## macOS notes

The first capture triggers a microphone permission prompt (expected). Release
binaries are unsigned in v0, so Gatekeeper quarantines them; clear it with:

```
xattr -d com.apple.quarantine ./voxthief
```

## Development

Building from source is only needed if you're hacking on voxthief or targeting a
platform without a release binary. voxthief statically links whisper.cpp via
cgo, so a build needs **Go 1.26+**, **cmake**, and a **C/C++ toolchain**:

| Platform | Toolchain |
|---|---|
| macOS | `xcode-select --install` and `brew install cmake` |
| Linux | `sudo apt install build-essential cmake` (or the dnf/pacman equivalent) |
| Windows | MSYS2 with `mingw-w64-ucrt-x86_64-gcc` and `-cmake` (cgo does not use MSVC) |

Then the build is the same everywhere:

```
git clone --recurse-submodules https://github.com/pid1/voxthief
cd voxthief
make build          # builds whisper.cpp statically, then the binary
./voxthief version
```

Other Makefile targets (identical across platforms):

| Target | What it does |
|---|---|
| `make build` | Static whisper.cpp + the voxthief binary (`-tags whisper`) |
| `make build-nowhisper` | Capture-only build with no C toolchain (no transcription) |
| `make whisper` | Just the whisper.cpp static libraries |
| `make test` | `go test -race ./...` |
| `make lint` | golangci-lint |
| `make generate` | Regenerate sqlc code |

Releases are produced automatically: every merge to `main` builds and publishes
tagged binaries for all five targets above via GitHub Actions.

## Legal

Monitoring amateur bands you are licensed for, and unencrypted public-safety
traffic, is legal in most US states — but laws vary by jurisdiction. This is a
short disclaimer, not legal advice; know your local rules before you listen or
record.

## License

BSD 3-Clause. See [LICENSE](LICENSE).
