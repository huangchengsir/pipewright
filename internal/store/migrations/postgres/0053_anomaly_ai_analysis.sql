-- PostgreSQL: explicit schema, integer booleans, byte-preserving ciphertext.
ALTER TABLE anomaly_rules ADD COLUMN ai_analysis_enabled BIGINT NOT NULL DEFAULT 0;
ALTER TABLE anomaly_alerts ADD COLUMN rule_id TEXT COLLATE "C" NOT NULL DEFAULT '';
ALTER TABLE anomaly_alerts ADD COLUMN ai_analysis_enabled BIGINT NOT NULL DEFAULT 0;
ALTER TABLE anomaly_alerts ADD COLUMN analysis_status TEXT COLLATE "C" NOT NULL DEFAULT '';
ALTER TABLE anomaly_alerts ADD COLUMN analysis_json TEXT COLLATE "C";
