-- GatewayOps user risk assessment configuration
-- AutoMigrate also creates this column; this file is for manual schema review/deployment.
ALTER TABLE relay_stations
    ADD COLUMN IF NOT EXISTS user_risk_config_json TEXT;
