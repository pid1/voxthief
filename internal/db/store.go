// Package db is the SQLite data layer: embedded goose migrations, sqlc-generated
// queries (internal/db/gen), and a Store that enforces the pragmas and
// single-writer discipline of §3.2 / §8.1.
package db

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/pid1/voxthief/internal/db/gen"
	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Store owns the database connection pool and serializes writes behind a mutex
// to honor single-writer discipline while WAL keeps readers non-blocking.
type Store struct {
	db  *sql.DB
	q   *gen.Queries
	wmu sync.Mutex
}

// Open opens (creating parent dirs) the SQLite database at path with WAL,
// busy_timeout, and foreign_keys enabled on every connection.
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	dsn := "file:" + url.PathEscape(path) +
		"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(on)"
	sdb, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// Single connection for the whole process: guarantees read-your-writes and
	// matches the single-writer discipline (§3.2). WAL still lets a SEPARATE
	// process (e.g. `voxthief export`) read concurrently.
	sdb.SetMaxOpenConns(1)
	if err := sdb.PingContext(context.Background()); err != nil {
		_ = sdb.Close()
		return nil, err
	}
	return &Store{db: sdb, q: gen.New(sdb)}, nil
}

func (s *Store) Close() error { return s.db.Close() }

// DB exposes the raw handle for goose operations.
func (s *Store) DB() *sql.DB { return s.db }

func provider(sdb *sql.DB) (*goose.Provider, error) {
	sub, err := newSubFS()
	if err != nil {
		return nil, err
	}
	return goose.NewProvider(goose.DialectSQLite3, sdb, sub)
}

// Upgrade applies all pending migrations and returns the resulting version.
func (s *Store) Upgrade(ctx context.Context) (int64, error) {
	p, err := provider(s.db)
	if err != nil {
		return 0, err
	}
	if _, err := p.Up(ctx); err != nil {
		return 0, err
	}
	return p.GetDBVersion(ctx)
}

// Status reports the current DB version and the head (latest available)
// version.
func (s *Store) Status(ctx context.Context) (current, head int64, err error) {
	p, perr := provider(s.db)
	if perr != nil {
		return 0, 0, perr
	}
	current, err = p.GetDBVersion(ctx)
	if err != nil {
		return 0, 0, err
	}
	for _, src := range p.ListSources() {
		if src.Version > head {
			head = src.Version
		}
	}
	return current, head, nil
}

// EnsureHead returns an error (with the one-line fix) if the DB is not migrated
// to the latest version. `voxthief listen` calls this at startup (§8.1).
func (s *Store) EnsureHead(ctx context.Context) error {
	current, head, err := s.Status(ctx)
	if err != nil {
		return err
	}
	if current < head {
		return fmt.Errorf("database is at migration %d but head is %d; run: voxthief db upgrade", current, head)
	}
	return nil
}

// --- write path (single-writer discipline) ---

// TransmissionMeta is the initial pending row written when a segment closes.
type TransmissionMeta struct {
	StartedAt   time.Time
	EndedAt     time.Time
	Duration    time.Duration
	Source      string
	FrequencyHz *int64
	AudioPath   string
	Model       string
	Capped      bool
}

func nullFreq(f *int64) sql.NullInt64 {
	if f == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: *f, Valid: true}
}

func nullStr(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

func nullF64(f *float64) sql.NullFloat64 {
	if f == nil {
		return sql.NullFloat64{}
	}
	return sql.NullFloat64{Float64: *f, Valid: true}
}

func b2i(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

// InsertPending writes a pending transmission row and returns its id.
func (s *Store) InsertPending(ctx context.Context, m TransmissionMeta) (int64, error) {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	return s.q.InsertTransmission(ctx, gen.InsertTransmissionParams{
		StartedAt:   unix(m.StartedAt),
		EndedAt:     unix(m.EndedAt),
		DurationS:   m.Duration.Seconds(),
		Source:      m.Source,
		FrequencyHz: nullFreq(m.FrequencyHz),
		AudioPath:   nullStr(m.AudioPath),
		Model:       m.Model,
		Capped:      b2i(m.Capped),
		Status:      "pending",
		CreatedAt:   unix(time.Now().UTC()),
	})
}

// TranscriptionResult is the outcome written after transcription.
type TranscriptionResult struct {
	Text             string
	Language         string
	AvgLogprob       *float64
	NoSpeechProb     *float64
	CompressionRatio *float64
	Status           string // transcribed | filtered
	FilterReason     string
}

func (s *Store) FinishTranscription(ctx context.Context, id int64, r TranscriptionResult) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	return s.q.FinishTranscription(ctx, gen.FinishTranscriptionParams{
		Text:             nullStr(r.Text),
		Language:         nullStr(r.Language),
		AvgLogprob:       nullF64(r.AvgLogprob),
		NoSpeechProb:     nullF64(r.NoSpeechProb),
		CompressionRatio: nullF64(r.CompressionRatio),
		Status:           r.Status,
		FilterReason:     nullStr(r.FilterReason),
		ID:               id,
	})
}

func (s *Store) SetError(ctx context.Context, id int64, msg string) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	return s.q.SetTransmissionError(ctx, gen.SetTransmissionErrorParams{
		Error: nullStr(msg), ID: id,
	})
}

// Segment is one transcription segment to persist.
type Segment struct {
	StartS     float64
	EndS       float64
	Text       string
	AvgLogprob *float64
}

func (s *Store) InsertSegment(ctx context.Context, transmissionID int64, seg Segment) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	return s.q.InsertSegment(ctx, gen.InsertSegmentParams{
		TransmissionID: transmissionID,
		StartS:         seg.StartS,
		EndS:           seg.EndS,
		Text:           seg.Text,
		AvgLogprob:     nullF64(seg.AvgLogprob),
	})
}

// AlertRecord is a row for the alerts table (§8).
type AlertRecord struct {
	TransmissionID int64
	RuleNames      string
	SentAt         *time.Time
	Status         string // sent | failed | suppressed
	SuppressReason string // cooldown | hourly_cap
	HTTPStatus     *int
	Error          string
}

func (s *Store) InsertAlert(ctx context.Context, a AlertRecord) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	var sentAt sql.NullFloat64
	if a.SentAt != nil {
		sentAt = sql.NullFloat64{Float64: unix(*a.SentAt), Valid: true}
	}
	var httpStatus sql.NullInt64
	if a.HTTPStatus != nil {
		httpStatus = sql.NullInt64{Int64: int64(*a.HTTPStatus), Valid: true}
	}
	return s.q.InsertAlert(ctx, gen.InsertAlertParams{
		TransmissionID: a.TransmissionID,
		RuleNames:      a.RuleNames,
		SentAt:         sentAt,
		Status:         a.Status,
		SuppressReason: nullStr(a.SuppressReason),
		HttpStatus:     httpStatus,
		Error:          nullStr(a.Error),
	})
}

// CountAlertsSentSince supports the global hourly sliding-window cap (§7.2).
func (s *Store) CountAlertsSentSince(ctx context.Context, since time.Time) (int, error) {
	n, err := s.q.CountAlertsSentSince(ctx, unix(since))
	return int(n), err
}

// LastFireForRule returns the last sent time for a rule (cooldown, §7.2), or
// zero time if never. Matching uses a LIKE on the comma-joined rule_names.
func (s *Store) LastFireForRule(ctx context.Context, rule string) (time.Time, error) {
	v, err := s.q.LastRuleFireAt(ctx, "%"+rule+"%")
	if err != nil {
		return time.Time{}, err
	}
	if !v.Valid {
		return time.Time{}, nil
	}
	return fromUnix(v.Float64), nil
}

// --- read path ---

func (s *Store) GetTransmission(ctx context.Context, id int64) (gen.Transmission, error) {
	return s.q.GetTransmission(ctx, id)
}

func (s *Store) ListTransmissionsSince(ctx context.Context, from, to time.Time, includeFiltered bool) ([]gen.Transmission, error) {
	return s.q.ListTransmissionsSince(ctx, gen.ListTransmissionsSinceParams{
		FromStartedAt:   unix(from),
		ToStartedAt:     unix(to),
		IncludeFiltered: b2i(includeFiltered),
	})
}

// unix converts a time to a REAL epoch (subsecond). It splits seconds from
// nanoseconds rather than using UnixNano so it stays correct for epochs beyond
// 2262 and never funnels a huge value through a float→int64 conversion.
func unix(t time.Time) float64 {
	t = t.UTC()
	return float64(t.Unix()) + float64(t.Nanosecond())/1e9
}

// fromUnix is the inverse of unix. Crucially it converts the (in-range) whole
// seconds to int64 directly instead of computing int64(f*1e9): for a large f
// (e.g. a far-future query bound like 1e12), f*1e9 overflows int64, and an
// out-of-range float→int64 conversion is platform-dependent — arm64 saturates
// to MaxInt64 while amd64 wraps to MinInt64. That asymmetry silently turned an
// upper time bound into a negative time on Linux, dropping every row (only in
// CI). Splitting seconds from the fraction keeps every conversion in range.
func fromUnix(f float64) time.Time {
	sec := math.Floor(f)
	nsec := math.Round((f - sec) * 1e9)
	return time.Unix(int64(sec), int64(nsec)).UTC()
}

// FromUnix converts a stored REAL epoch to a time.Time (exported for callers
// rendering DB rows).
func FromUnix(f float64) time.Time { return fromUnix(f) }
