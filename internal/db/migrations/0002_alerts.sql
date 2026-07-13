-- +goose Up
-- Shipped as a separate migration deliberately, to exercise the upgrade path
-- early (§8).
CREATE TABLE alerts (
    id               INTEGER PRIMARY KEY,
    transmission_id  INTEGER NOT NULL REFERENCES transmissions (id) ON DELETE CASCADE,
    rule_names       TEXT    NOT NULL,
    sent_at          REAL,
    status           TEXT    NOT NULL, -- sent | failed | suppressed
    suppress_reason  TEXT,             -- cooldown | hourly_cap
    http_status      INTEGER,
    error            TEXT
);
CREATE INDEX idx_alerts_transmission_id ON alerts (transmission_id);
CREATE INDEX idx_alerts_sent_at ON alerts (sent_at);

-- +goose Down
DROP TABLE alerts;
