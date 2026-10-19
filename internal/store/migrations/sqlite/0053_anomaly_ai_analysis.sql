ALTER TABLE anomaly_rules ADD COLUMN ai_analysis_enabled INTEGER NOT NULL DEFAULT 0;
ALTER TABLE anomaly_alerts ADD COLUMN rule_id TEXT NOT NULL DEFAULT '';
ALTER TABLE anomaly_alerts ADD COLUMN ai_analysis_enabled INTEGER NOT NULL DEFAULT 0;
ALTER TABLE anomaly_alerts ADD COLUMN analysis_status TEXT NOT NULL DEFAULT '';
ALTER TABLE anomaly_alerts ADD COLUMN analysis_json TEXT;
