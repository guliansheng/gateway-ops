-- Group degradation configuration/history are independent of replaceable group snapshots.
-- Matches storage.GroupDegradationConfig / GroupDegradationResult in AutoMigrate.
CREATE TABLE IF NOT EXISTS group_degradation_configs (
    id BIGSERIAL PRIMARY KEY,
    station_id BIGINT NOT NULL,
    group_external_id BIGINT NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT FALSE,
    method VARCHAR(16) NOT NULL DEFAULT 'pelican',
    model VARCHAR(256) NOT NULL DEFAULT '',
    judge_model VARCHAR(256) NOT NULL DEFAULT '',
    effort VARCHAR(16) NOT NULL DEFAULT 'medium',
    interval_minutes BIGINT NOT NULL DEFAULT 15,
    preview_mode VARCHAR(16) NOT NULL DEFAULT 'hover',
    last_run_at TIMESTAMPTZ,
    next_run_at TIMESTAMPTZ,
    lease_token VARCHAR(64) NOT NULL DEFAULT '',
    lease_until TIMESTAMPTZ,
    created_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ,
    CONSTRAINT chk_group_degradation_method CHECK (method IN ('pelican', 'candy')),
    CONSTRAINT chk_group_degradation_effort CHECK (effort IN ('none', 'low', 'medium', 'high')),
    CONSTRAINT chk_group_degradation_interval CHECK (interval_minutes BETWEEN 5 AND 1440),
    CONSTRAINT chk_group_degradation_preview CHECK (preview_mode IN ('hover', 'click'))
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_group_degradation_scope
    ON group_degradation_configs (station_id, group_external_id);
CREATE INDEX IF NOT EXISTS idx_group_degradation_due
    ON group_degradation_configs (enabled, next_run_at);

CREATE TABLE IF NOT EXISTS group_degradation_results (
    id BIGSERIAL PRIMARY KEY,
    station_id BIGINT NOT NULL,
    group_external_id BIGINT NOT NULL,
    method VARCHAR(16) NOT NULL,
    model VARCHAR(256) NOT NULL,
    judge_model VARCHAR(256) NOT NULL,
    effort VARCHAR(16) NOT NULL,
    status VARCHAR(16) NOT NULL,
    reason TEXT NOT NULL,
    html TEXT NOT NULL DEFAULT '',
    answer TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT chk_group_degradation_result_method CHECK (method IN ('pelican', 'candy')),
    CONSTRAINT chk_group_degradation_result_effort CHECK (effort IN ('none', 'low', 'medium', 'high')),
    CONSTRAINT chk_group_degradation_result_status CHECK (status IN ('normal', 'degraded', 'error', 'inconclusive'))
);
CREATE INDEX IF NOT EXISTS idx_group_degradation_results
    ON group_degradation_results (station_id, group_external_id, created_at DESC);
