-- Only daily baselines observed before/exactly at midnight can reconstruct
-- consumption with recharges after their source snapshots have been cleaned up.
-- Legacy rows have no source-time evidence and must remain untrusted.
-- The server also adds this column through AutoMigrate at startup.
BEGIN;
ALTER TABLE channel_daily_balances
    ADD COLUMN IF NOT EXISTS reliable BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS source_sampled_at TIMESTAMPTZ;
COMMIT;
