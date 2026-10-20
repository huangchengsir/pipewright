ALTER TABLE anomaly_rules ADD COLUMN ai_analysis_enabled TINYINT NOT NULL DEFAULT 0;
ALTER TABLE anomaly_alerts ADD COLUMN rule_id VARCHAR(36) NOT NULL DEFAULT '';
ALTER TABLE anomaly_alerts ADD COLUMN ai_analysis_enabled TINYINT NOT NULL DEFAULT 0;
ALTER TABLE anomaly_alerts ADD COLUMN analysis_status VARCHAR(16) NOT NULL DEFAULT '';
ALTER TABLE anomaly_alerts ADD COLUMN analysis_json LONGTEXT NULL;
