-- PostgreSQL: explicit schema, integer booleans, byte-preserving ciphertext.
CREATE TABLE selfupdate_tasks (
 id TEXT COLLATE "C" PRIMARY KEY, instance_id TEXT COLLATE "C" NOT NULL, request_id TEXT COLLATE "C" NOT NULL,
 target_tag TEXT COLLATE "C" NOT NULL, status TEXT COLLATE "C" NOT NULL, active_key TEXT COLLATE "C" UNIQUE,
 record_json TEXT COLLATE "C" NOT NULL, created_at BIGINT NOT NULL,
 UNIQUE(instance_id,request_id)
);
CREATE TABLE selfupdate_control (id BIGINT PRIMARY KEY,task_id TEXT COLLATE "C" NOT NULL DEFAULT '');
INSERT INTO selfupdate_control(id,task_id) VALUES(1,'');
CREATE TABLE selfupdate_leases(id TEXT COLLATE "C" PRIMARY KEY,kind TEXT COLLATE "C" NOT NULL,created_at BIGINT NOT NULL);
