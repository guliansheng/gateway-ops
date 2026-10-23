-- GatewayOps Hub-local group model pricing visibility.
-- Run only once when the column is absent: existing public groups are enabled,
-- exclusive groups are disabled. Later runs must preserve manually saved false.
DO $$
BEGIN
    IF to_regclass('relay_groups') IS NOT NULL AND NOT EXISTS (
        SELECT 1 FROM pg_attribute
        WHERE attrelid = 'relay_groups'::regclass
          AND attname = 'model_pricing_enabled'
          AND NOT attisdropped
    ) THEN
        ALTER TABLE relay_groups
            ADD COLUMN model_pricing_enabled BOOLEAN NOT NULL DEFAULT false;
        IF EXISTS (
            SELECT 1 FROM pg_attribute
            WHERE attrelid = 'relay_groups'::regclass
              AND attname = 'is_exclusive'
              AND NOT attisdropped
        ) THEN
            UPDATE relay_groups
            SET model_pricing_enabled = NOT COALESCE(is_exclusive, false);
        ELSE
            UPDATE relay_groups SET model_pricing_enabled = true;
        END IF;
    END IF;
END
$$;
