-- 0032 project_runners:项目级「远程构建 runner」配置(FR-8-14 远程 runner 池续)。
-- 每项目至多一行(project_id 主键+外键)。runner_server_id 指定一台已登记的目标服务器作该项目的
-- 远程构建机:有则构建下沉到该机执行(控制机本地克隆→经 SSH 传产物→远程 docker 跑),空/无行 = 本地构建。
-- 不存敏感信息;删项目级联删 runner 配置。runner_server_id 不设 FK 到 servers(允许先配后建/换机;
-- 使用时由服务层校验存在),仅删项目时清本配置。
--   project_id       : FK → projects.id;PK;ON DELETE CASCADE
--   runner_server_id : 远程构建机服务器 id(target server);空串 = 本地构建

CREATE TABLE IF NOT EXISTS project_runners (
    project_id       TEXT PRIMARY KEY,
    runner_server_id TEXT NOT NULL DEFAULT '',
    created_at       TEXT NOT NULL,
    updated_at       TEXT NOT NULL,
    FOREIGN KEY (project_id) REFERENCES projects (id) ON DELETE CASCADE
);
