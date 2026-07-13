package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"

	tea "charm.land/bubbletea/v2"
	"github.com/spf13/cobra"

	"github.com/pid1/voxthief/internal/alerts"
	"github.com/pid1/voxthief/internal/asr"
	"github.com/pid1/voxthief/internal/audio"
	"github.com/pid1/voxthief/internal/config"
	"github.com/pid1/voxthief/internal/db"
	"github.com/pid1/voxthief/internal/events"
	"github.com/pid1/voxthief/internal/pipeline"
	"github.com/pid1/voxthief/internal/tui"
)

func newListenCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "listen",
		Short: "Capture, transcribe, and (optionally) alert on radio traffic",
		RunE:  runListen,
	}
	cmd.Flags().String("input", "", "input spec: soundcard[:SEL] | rtlsdr:FREQ")
	cmd.Flags().Bool("headless", false, "run without the TUI, emitting JSONL on stdout")
	cmd.Flags().String("db", "", "database path (overrides config)")
	cmd.Flags().String("model", "", "whisper model name (overrides config)")
	cmd.Flags().Float64("threshold", 0, "RMS squelch threshold in dBFS (overrides config)")
	cmd.Flags().Bool("verbose", false, "verbose logging")
	addSDRFlags(cmd)
	return cmd
}

func runListen(cmd *cobra.Command, _ []string) error {
	cfg, _, err := loadConfig(cmd)
	if err != nil {
		return err
	}
	applyListenOverrides(cmd, &cfg)

	source, err := buildSource(cmd, cfg)
	if err != nil {
		return err
	}

	// Store: open and refuse to start unless migrated to head (§8.1).
	dbPath, err := cfg.ResolveDBPath()
	if err != nil {
		return err
	}
	store, err := db.Open(dbPath)
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()
	if err := store.EnsureHead(cmd.Context()); err != nil {
		return err
	}

	// Logging: in TUI mode never write to the terminal (it corrupts the alt
	// screen) — log to a file next to the DB. Headless keeps stderr.
	headless, _ := cmd.Flags().GetBool("headless")
	verbose, _ := cmd.Flags().GetBool("verbose")
	if logFile := setupLogging(headless, verbose, filepath.Join(filepath.Dir(dbPath), "voxthief.log")); logFile != nil {
		defer func() { _ = logFile.Close() }()
	}

	// Audio retention: prune old WAVs at startup (§5).
	audioDir := ""
	if cfg.Storage.RetainAudio {
		if audioDir, err = cfg.ResolveAudioDir(); err != nil {
			return err
		}
		if _, err := pipeline.PruneAudio(audioDir, cfg.Storage.RetentionDays); err != nil {
			fmt.Fprintln(os.Stderr, "audio prune:", err)
		}
	}

	// Alerts (optional): compile rules and start a dispatcher (§7).
	var rules []alerts.Rule
	var dispatcher *alerts.Dispatcher

	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Emit sink is wired below once we know TUI vs headless.
	var emit func(any)

	if cfg.Alerts.Enabled {
		rules, err = alerts.Compile(cfg.Alerts.Rules)
		if err != nil {
			return err
		}
		client := alerts.NewClient(cfg.Pushover.Token, cfg.Pushover.UserKey)
		dispatcher = alerts.NewDispatcher(client, rules, store, cfg.Alerts.MaxPerHour, func(m events.AlertMsg) {
			if emit != nil {
				emit(m)
			}
		})
	}

	newTr := transcriberFactory(ctx, cfg, func(m any) {
		if emit != nil {
			emit(m)
		}
	})

	opts := pipeline.Options{
		Source: source,
		Seg: audio.Params{
			ThresholdDBFS: cfg.Audio.ThresholdDBFS,
			OpenBlocks:    cfg.Audio.OpenBlocks,
			HangTimeS:     cfg.Audio.HangTimeS,
			PrerollMS:     cfg.Audio.PrerollMS,
			MaxSegmentS:   cfg.Audio.MaxSegmentS,
			MinSegmentS:   cfg.Audio.MinSegmentS,
		},
		NewTranscriber: newTr,
		Workers:        cfg.Transcription.Workers,
		Store:          store,
		FilterCfg:      asr.DefaultFilterConfig(cfg.Transcription.Blocklist...),
		Rules:          rules,
		Dispatcher:     dispatcher,
		Model:          cfg.Transcription.Model,
		RetainAudio:    cfg.Storage.RetainAudio,
		AudioDir:       audioDir,
		Verbose:        verbose,
	}

	if headless {
		emit = pipeline.NewHeadlessEmitter(os.Stdout, os.Stderr, verbose)
		opts.Emit = emit
		return pipeline.Run(ctx, opts)
	}
	return runTUI(ctx, stop, cfg, source, opts, &emit)
}

// runTUI starts the Bubble Tea program and the pipeline together, wiring the
// program's Send as the event sink and draining on quit (§9).
func runTUI(ctx context.Context, stop context.CancelFunc, cfg config.Config, source audio.AudioSource, opts pipeline.Options, emit *func(any)) error {
	pipeCtx, cancelPipe := context.WithCancel(ctx)

	model := tui.New(tui.Config{
		Input: source.Descriptor(),
		Model: cfg.Transcription.Model,
	}, cancelPipe) // onQuit cancels the pipeline context so it drains

	prog := tea.NewProgram(model, tea.WithContext(ctx))
	*emit = func(m any) { prog.Send(m) }
	opts.Emit = *emit

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := pipeline.Run(pipeCtx, opts); err != nil {
			prog.Send(events.StatusMsg{Text: err.Error(), Warn: true})
		}
	}()

	// A signal (SIGTERM) cancels ctx; ask the program to quit gracefully.
	go func() {
		<-ctx.Done()
		prog.Quit()
	}()

	_, runErr := prog.Run()
	cancelPipe() // ensure the pipeline drains even on a program error
	stop()
	wg.Wait()
	return runErr
}

// setupLogging directs slog output. In headless mode it uses stderr (verbose →
// debug). In TUI mode it writes to a log file next to the DB, because ANY write
// to the terminal corrupts the Bubble Tea alt screen. Returns the open log file
// (if any) for the caller to close.
func setupLogging(headless, verbose bool, logPath string) *os.File {
	level := slog.LevelInfo
	if verbose {
		level = slog.LevelDebug
	}
	if headless {
		slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})))
		return nil
	}
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
		return nil
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(f, &slog.HandlerOptions{Level: level})))
	return f
}

func applyListenOverrides(cmd *cobra.Command, cfg *config.Config) {
	if f := cmd.Flags().Lookup("db"); f != nil && f.Changed {
		cfg.Storage.DBPath, _ = cmd.Flags().GetString("db")
	}
	if f := cmd.Flags().Lookup("model"); f != nil && f.Changed {
		cfg.Transcription.Model, _ = cmd.Flags().GetString("model")
	}
	if f := cmd.Flags().Lookup("threshold"); f != nil && f.Changed {
		cfg.Audio.ThresholdDBFS, _ = cmd.Flags().GetFloat64("threshold")
	}
}

// transcriberFactory returns a factory that builds a transcriber per worker.
// Without whisper support (default build) it returns a NullTranscriber so
// capture still runs; with whisper it ensures the models then loads them.
func transcriberFactory(ctx context.Context, cfg config.Config, emit func(any)) func() (asr.Transcriber, error) {
	return func() (asr.Transcriber, error) {
		if !asr.Available {
			emit(events.StatusMsg{Text: "built without whisper: capturing only, no transcription", Warn: true})
			return asr.NullTranscriber{}, nil
		}
		modelsDir, err := config.ResolveModelsDir()
		if err != nil {
			return nil, err
		}
		modelPath, err := asr.EnsureModel(ctx, asr.ModelFilename(cfg.Transcription.Model), modelsDir, func(m events.ModelProgressMsg) { emit(m) })
		if err != nil {
			return nil, err
		}
		vadPath, err := asr.EnsureModel(ctx, asr.VADModelFilename, modelsDir, func(m events.ModelProgressMsg) { emit(m) })
		if err != nil {
			return nil, err
		}
		return asr.New(asr.Params{
			ModelPath:     modelPath,
			VADModelPath:  vadPath,
			BeamSize:      cfg.Transcription.BeamSize,
			Language:      cfg.Transcription.Language,
			InitialPrompt: cfg.Transcription.InitialPrompt,
			NoContext:     true,
			Threads:       asr.DefaultThreads(),
			VAD:           true,
		})
	}
}
