-- Station-owned admin-only prompt settings. Logical defaults live in Go;
-- reads and migrations deliberately do not insert default settings rows.
-- Matches storage.DegradationPromptSettings / GroupDegradationConfig AutoMigrate.
BEGIN;

CREATE TABLE IF NOT EXISTS degradation_prompt_settings (
    id BIGSERIAL PRIMARY KEY,
    station_id BIGINT NOT NULL,
    candy_prompt TEXT NOT NULL,
    candy_expected_answer TEXT NOT NULL,
    pelican_prompt TEXT NOT NULL,
    created_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_degradation_prompt_station
    ON degradation_prompt_settings (station_id);

ALTER TABLE group_degradation_configs
    ADD COLUMN IF NOT EXISTS history_limit BIGINT NOT NULL DEFAULT 100;
-- Only legacy zero/unset values are repaired; repeated execution must never
-- replace an administrator's positive retention choice or rewrite prompts.
UPDATE group_degradation_configs SET history_limit = 100
    WHERE history_limit = 0 OR history_limit IS NULL;
ALTER TABLE group_degradation_configs
    ALTER COLUMN history_limit SET DEFAULT 100,
    ALTER COLUMN history_limit SET NOT NULL;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'chk_group_degradation_history_limit'
          AND conrelid = 'group_degradation_configs'::regclass
    ) THEN
        ALTER TABLE group_degradation_configs
            ADD CONSTRAINT chk_group_degradation_history_limit
            CHECK (history_limit BETWEEN 1 AND 1000);
    END IF;
END $$;

COMMIT;
