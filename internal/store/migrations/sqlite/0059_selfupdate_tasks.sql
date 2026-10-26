CREATE TABLE selfupdate_tasks (
 id TEXT PRIMARY KEY, instance_id TEXT NOT NULL, request_id TEXT NOT NULL,
 target_tag TEXT NOT NULL, status TEXT NOT NULL, active_key TEXT UNIQUE,
 record_json TEXT NOT NULL, created_at INTEGER NOT NULL,
 UNIQUE(instance_id,request_id)
);
CREATE TABLE selfupdate_control (id INTEGER PRIMARY KEY,task_id TEXT NOT NULL DEFAULT '');
INSERT INTO selfupdate_control(id,task_id) VALUES(1,'');
CREATE TABLE selfupdate_leases(id TEXT PRIMARY KEY,kind TEXT NOT NULL,created_at INTEGER NOT NULL);
