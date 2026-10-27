CREATE TABLE selfupdate_tasks (
 id VARCHAR(36) PRIMARY KEY, instance_id VARCHAR(64) NOT NULL, request_id VARBINARY(128) NOT NULL,
 target_tag VARCHAR(128) NOT NULL, status VARCHAR(32) NOT NULL, active_key VARCHAR(64) UNIQUE,
 record_json MEDIUMTEXT NOT NULL, created_at BIGINT NOT NULL,
 UNIQUE(instance_id,request_id)
);
CREATE TABLE selfupdate_control (id INT PRIMARY KEY,task_id VARCHAR(36) NOT NULL DEFAULT '');
INSERT INTO selfupdate_control(id,task_id) VALUES(1,'');
CREATE TABLE selfupdate_leases(id VARCHAR(36) PRIMARY KEY,kind VARCHAR(32) NOT NULL,created_at BIGINT NOT NULL);
