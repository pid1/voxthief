-- queries.sql — source for sqlc (config in sqlc.yaml). Generated code is
-- committed under internal/db/gen. Regenerate with `make generate`.

-- name: InsertTransmission :one
INSERT INTO transmissions (
    started_at, ended_at, duration_s, source, frequency_hz, audio_path,
    model, capped, status, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING id;

-- name: FinishTranscription :exec
UPDATE transmissions
SET text = ?, language = ?, avg_logprob = ?, no_speech_prob = ?,
    compression_ratio = ?, status = ?, filter_reason = ?
WHERE id = ?;

-- name: SetTransmissionError :exec
UPDATE transmissions
SET status = 'error', error = ?
WHERE id = ?;

-- name: GetTransmission :one
SELECT * FROM transmissions WHERE id = ?;

-- name: ListTransmissionsSince :many
SELECT * FROM transmissions
WHERE started_at >= ? AND started_at < ?
  AND (status = 'transcribed' OR ? = 1)
ORDER BY started_at ASC;

-- name: InsertSegment :exec
INSERT INTO segments (transmission_id, start_s, end_s, text, avg_logprob)
VALUES (?, ?, ?, ?, ?);

-- name: ListSegments :many
SELECT * FROM segments WHERE transmission_id = ? ORDER BY start_s ASC;

-- name: InsertAlert :exec
INSERT INTO alerts (
    transmission_id, rule_names, sent_at, status, suppress_reason,
    http_status, error
) VALUES (?, ?, ?, ?, ?, ?, ?);

-- name: CountAlertsSentSince :one
SELECT COUNT(*) FROM alerts
WHERE status = 'sent' AND sent_at >= ?;

-- name: LastRuleFireAt :one
SELECT MAX(sent_at) FROM alerts
WHERE status = 'sent' AND rule_names LIKE ?;
