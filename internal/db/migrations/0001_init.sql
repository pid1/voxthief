-- +goose Up
CREATE TABLE transmissions (
    id                INTEGER PRIMARY KEY,
    started_at        REAL    NOT NULL,
    ended_at          REAL    NOT NULL,
    duration_s        REAL    NOT NULL,
    source            TEXT    NOT NULL,
    frequency_hz      INTEGER,
    audio_path        TEXT,
    text              TEXT,
    language          TEXT,
    model             TEXT    NOT NULL,
    avg_logprob       REAL,
    no_speech_prob    REAL,
    compression_ratio REAL,
    capped            INTEGER NOT NULL DEFAULT 0,
    status            TEXT    NOT NULL,
    filter_reason     TEXT,
    error             TEXT,
    created_at        REAL    NOT NULL
);
CREATE INDEX idx_transmissions_started_at ON transmissions (started_at);
CREATE INDEX idx_transmissions_status ON transmissions (status);

CREATE TABLE segments (
    id               INTEGER PRIMARY KEY,
    transmission_id  INTEGER NOT NULL REFERENCES transmissions (id) ON DELETE CASCADE,
    start_s          REAL    NOT NULL,
    end_s            REAL    NOT NULL,
    text             TEXT    NOT NULL,
    avg_logprob      REAL
);
CREATE INDEX idx_segments_transmission_id ON segments (transmission_id);

-- +goose Down
DROP TABLE segments;
DROP TABLE transmissions;
