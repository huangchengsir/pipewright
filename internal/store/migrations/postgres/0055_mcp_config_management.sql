-- PostgreSQL: explicit schema, integer booleans, byte-preserving ciphertext.
ALTER TABLE mcp_tokens ADD COLUMN config_grants_json TEXT COLLATE "C" NOT NULL DEFAULT '[]';
ALTER TABLE mcp_tokens ADD COLUMN authz_revision BIGINT NOT NULL DEFAULT 0;

CREATE TABLE project_config_versions (
 project_id TEXT COLLATE "C" PRIMARY KEY , revision BIGINT NOT NULL DEFAULT 1,
 deleted_at BIGINT
);
INSERT INTO project_config_versions(project_id,revision) SELECT id,1 FROM projects;
CREATE TABLE config_control_epochs (name TEXT COLLATE "C" PRIMARY KEY ,value BIGINT NOT NULL DEFAULT 1);
INSERT INTO config_control_epochs(name,value) VALUES ('policy',1),('secret_catalog',1),('executor',1);

CREATE TABLE project_activity (
 activity_id TEXT COLLATE "C" PRIMARY KEY ,project_id TEXT COLLATE "C" NOT NULL ,
 run_id TEXT COLLATE "C",owner_epoch TEXT COLLATE "C" NOT NULL ,kind TEXT COLLATE "C" NOT NULL,phase TEXT COLLATE "C" NOT NULL,
 created_at BIGINT NOT NULL,heartbeat_at BIGINT NOT NULL,released_at BIGINT
);
CREATE INDEX project_activity_project ON project_activity(project_id,released_at);
CREATE INDEX project_activity_run ON project_activity(run_id,released_at);
INSERT INTO project_activity(activity_id,project_id,run_id,owner_epoch,kind,phase,created_at,heartbeat_at)
 SELECT 'legacy-' || id,project_id,id,'legacy-unreconciled','run','cleanup_unknown',0,0
 FROM pipeline_runs WHERE status IN ('queued','running','waiting_approval');

CREATE TABLE mcp_config_drafts (
 id TEXT COLLATE "C" PRIMARY KEY ,owner_token_id TEXT COLLATE "C" NOT NULL ,
 project_id TEXT COLLATE "C" NOT NULL ,request_id TEXT COLLATE "C" NOT NULL ,
 input_digest TEXT COLLATE "C" NOT NULL,base_revision BIGINT NOT NULL,policy_version TEXT COLLATE "C" NOT NULL,
 validator_version TEXT COLLATE "C" NOT NULL,authz_revision BIGINT NOT NULL,
 operations_json TEXT COLLATE "C" NOT NULL,patch_digest TEXT COLLATE "C" NOT NULL,created_at BIGINT NOT NULL,
 expires_at BIGINT NOT NULL,applied_operation_id TEXT COLLATE "C",
 UNIQUE(owner_token_id,request_id)
);
CREATE INDEX mcp_config_drafts_owner ON mcp_config_drafts(owner_token_id,project_id,expires_at);
CREATE TABLE mcp_config_approvals (
 id TEXT COLLATE "C" PRIMARY KEY ,draft_id TEXT COLLATE "C" NOT NULL ,
 token_id TEXT COLLATE "C" NOT NULL ,project_id TEXT COLLATE "C" NOT NULL ,
 base_revision BIGINT NOT NULL,policy_version TEXT COLLATE "C" NOT NULL,validator_version TEXT COLLATE "C" NOT NULL,
 authz_revision BIGINT NOT NULL,patch_digest TEXT COLLATE "C" NOT NULL,review_digest TEXT COLLATE "C" NOT NULL,
 approver TEXT COLLATE "C" NOT NULL,created_at BIGINT NOT NULL,expires_at BIGINT NOT NULL,
 revoked_at BIGINT,consumed_by_operation_id TEXT COLLATE "C"
);
CREATE INDEX mcp_config_approvals_draft ON mcp_config_approvals(draft_id,expires_at);
CREATE TABLE mcp_config_operations (
 id TEXT COLLATE "C" PRIMARY KEY ,token_id TEXT COLLATE "C" NOT NULL ,
 project_id TEXT COLLATE "C" NOT NULL ,request_id TEXT COLLATE "C" NOT NULL ,
 input_digest TEXT COLLATE "C" NOT NULL,draft_id TEXT COLLATE "C" NOT NULL,approval_id TEXT COLLATE "C" NOT NULL,
 owner_epoch TEXT COLLATE "C" NOT NULL,state TEXT COLLATE "C" NOT NULL DEFAULT 'pending',reason TEXT COLLATE "C" NOT NULL DEFAULT '',
 before_revision BIGINT NOT NULL,after_revision BIGINT,result_json TEXT COLLATE "C" NOT NULL DEFAULT '{}',
 created_at BIGINT NOT NULL,updated_at BIGINT NOT NULL,
 UNIQUE(token_id,request_id)
);
CREATE INDEX mcp_config_operations_draft ON mcp_config_operations(draft_id);
