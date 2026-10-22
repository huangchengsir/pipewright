-- No cascading FKs: deleting a project or access token must not erase replay protection.
ALTER TABLE mcp_tokens ADD COLUMN config_grants_json TEXT NOT NULL DEFAULT ('[]');
ALTER TABLE mcp_tokens ADD COLUMN authz_revision BIGINT NOT NULL DEFAULT 0;

CREATE TABLE project_config_versions (
 project_id VARCHAR(36) CHARACTER SET ascii COLLATE ascii_bin PRIMARY KEY,
 revision BIGINT NOT NULL DEFAULT 1,deleted_at BIGINT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
INSERT INTO project_config_versions(project_id,revision) SELECT id,1 FROM projects;
CREATE TABLE config_control_epochs (
 name VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin PRIMARY KEY,value BIGINT NOT NULL DEFAULT 1
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
INSERT INTO config_control_epochs(name,value) VALUES ('policy',1),('secret_catalog',1),('executor',1);

CREATE TABLE project_activity (
 activity_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin PRIMARY KEY,
 project_id VARCHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 run_id VARCHAR(36) NULL,owner_epoch VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 kind VARCHAR(40) NOT NULL,phase VARCHAR(40) NOT NULL,created_at BIGINT NOT NULL,
 heartbeat_at BIGINT NOT NULL,released_at BIGINT NULL,
 INDEX project_activity_project(project_id,released_at),INDEX project_activity_run(run_id,released_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
INSERT INTO project_activity(activity_id,project_id,run_id,owner_epoch,kind,phase,created_at,heartbeat_at)
 SELECT CONCAT('legacy-',id),project_id,id,'legacy-unreconciled','run','cleanup_unknown',0,0
 FROM pipeline_runs WHERE status IN ('queued','running','waiting_approval');

CREATE TABLE mcp_config_drafts (
 id VARCHAR(36) CHARACTER SET ascii COLLATE ascii_bin PRIMARY KEY,
 owner_token_id VARCHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 project_id VARCHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 request_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 input_digest CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 base_revision BIGINT NOT NULL,policy_version VARCHAR(128) NOT NULL,
 validator_version VARCHAR(128) NOT NULL,authz_revision BIGINT NOT NULL,
 operations_json MEDIUMTEXT NOT NULL,patch_digest CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 created_at BIGINT NOT NULL,expires_at BIGINT NOT NULL,applied_operation_id VARCHAR(36) NULL,
 UNIQUE KEY mcp_config_drafts_request(owner_token_id,request_id),
 INDEX mcp_config_drafts_owner(owner_token_id,project_id,expires_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
CREATE TABLE mcp_config_approvals (
 id VARCHAR(36) CHARACTER SET ascii COLLATE ascii_bin PRIMARY KEY,
 draft_id VARCHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 token_id VARCHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 project_id VARCHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 base_revision BIGINT NOT NULL,policy_version VARCHAR(128) NOT NULL,
 validator_version VARCHAR(128) NOT NULL,authz_revision BIGINT NOT NULL,
 patch_digest CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 review_digest CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 approver VARCHAR(128) NOT NULL,created_at BIGINT NOT NULL,expires_at BIGINT NOT NULL,
 revoked_at BIGINT NULL,consumed_by_operation_id VARCHAR(36) NULL,
 INDEX mcp_config_approvals_draft(draft_id,expires_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
CREATE TABLE mcp_config_operations (
 id VARCHAR(36) CHARACTER SET ascii COLLATE ascii_bin PRIMARY KEY,
 token_id VARCHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 project_id VARCHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 request_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 input_digest CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 draft_id VARCHAR(36) NOT NULL,approval_id VARCHAR(36) NOT NULL,
 owner_epoch VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 state VARCHAR(24) NOT NULL DEFAULT 'pending',reason VARCHAR(64) NOT NULL DEFAULT '',
 before_revision BIGINT NOT NULL,after_revision BIGINT NULL,
 result_json MEDIUMTEXT NOT NULL,created_at BIGINT NOT NULL,updated_at BIGINT NOT NULL,
 UNIQUE KEY mcp_config_operations_request(token_id,request_id),INDEX mcp_config_operations_draft(draft_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
