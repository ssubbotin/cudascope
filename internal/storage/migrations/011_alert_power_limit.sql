-- The power limit a card was enforcing while a throttle event was open.
-- The threshold column holds 1 for those events, meaning "any reason bit is
-- set", and the journal had nothing to show under Threshold but a dash. A
-- power cap throttle does have a threshold: the card's own limit.
ALTER TABLE alert_events ADD COLUMN power_limit REAL;

-- Fill in what the samples still remember. Raw rows are kept for a day by
-- default, so this reaches the events of the last day and leaves the older
-- ones NULL, which reads as "not recorded" rather than "no limit". Without
-- it the column would stay empty until the next throttle, and the journal
-- would answer the question only for alerts that have not happened yet.
UPDATE alert_events
SET power_limit = (
    SELECT MAX(r.power_limit)
    FROM gpu_metrics_raw r
    WHERE COALESCE(r.node_id, 'local') = COALESCE(alert_events.node_id, 'local')
      AND r.gpu_id = alert_events.gpu_id
      AND r.ts >= alert_events.started_at
      AND r.ts <= COALESCE(alert_events.ended_at, alert_events.started_at + 86400)
      AND r.power_limit > 0
)
WHERE kind = 'throttled' AND gpu_id IS NOT NULL;

INSERT INTO schema_version (version) VALUES (11);
