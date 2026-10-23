-- Keep existing public previews visible while allowing status-only public results.
ALTER TABLE group_degradation_configs
    ADD COLUMN IF NOT EXISTS result_visibility VARCHAR(16) NOT NULL DEFAULT 'show';

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'chk_group_degradation_visibility'
          AND conrelid = 'group_degradation_configs'::regclass
    ) THEN
        ALTER TABLE group_degradation_configs
            ADD CONSTRAINT chk_group_degradation_visibility
            CHECK (result_visibility IN ('hide', 'show'));
    END IF;
END $$;
