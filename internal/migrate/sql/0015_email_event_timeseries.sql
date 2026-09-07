-- Lifetime event counts per bucket, for the analytics charts.
--
-- The third instance of one mistake, and the same one migration 0014 fixed for
-- the headline figures: the shared store answered GetTimeSeriesMetrics with
-- count(*) over the events table, which counts rows that are *retained*.
--
-- The Pebble store keeps timeseries:{tenant}:{bucket}:{type} counters and its
-- retention pass never touches them, so a chart of the last ninety days keeps
-- showing ninety days. Counting rows instead means every bucket older than
-- log_retention_days -- fourteen by default -- silently empties, and the chart
-- shows history disappearing rather than history.
--
-- One row per bucket per type per granularity, because Pebble keeps three
-- separate counter families and a chart asks for one of them.
--
-- Never decremented, for the same reason as 0014. Retention prunes events and
-- leaves these alone.

CREATE TABLE IF NOT EXISTS email_event_timeseries (
    tenant_id {{UUID}} NOT NULL,

    -- day, hour or minute. Stored rather than derived so a bucket string is
    -- never ambiguous about which family it belongs to: "2026-09-07 15" is an
    -- hour, and "2026-09-07" is a day, but nothing in the string says so.
    granularity TEXT NOT NULL,

    -- The formatted bucket, ISO-ordered so it sorts as text.
    bucket TEXT NOT NULL,

    type TEXT NOT NULL,
    count BIGINT NOT NULL DEFAULT 0,

    PRIMARY KEY (tenant_id, granularity, bucket, type)
);

-- A chart asks for one tenant, one granularity, and a range of buckets.
CREATE INDEX IF NOT EXISTS idx_email_event_timeseries_range
    ON email_event_timeseries (tenant_id, granularity, bucket);
