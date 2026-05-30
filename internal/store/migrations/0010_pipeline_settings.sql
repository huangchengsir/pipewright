-- 0010 pipeline_settings:项目构建/部署声明式配置(FR-5/FR-6/FR-7/部分 FR-9 · Story 2.4)。
-- 独立于 2-2 的 pipeline_configs(spec/canvas)。每项目一行(project_id 既是主键也是外键),
-- 记录构建配置(模型 A/B、产物类型、构建变量、依赖缓存)与环境定义(目标服务器引用占位、
-- 环境变量、镜像仓库绑定)。两块均存为结构化 JSON。
--
-- AC-SEC:secret 类型的构建变量 / 环境变量 / 镜像仓库凭据**只存 credentialId 引用**,
-- 引用保险库(credentials 表)里的密文凭据;本表 JSON 中**绝无任何明文 secret**
-- (非 secret 项才存明文 value)。响应掩码由服务端经保险库即时计算,不入库。
--   project_id        : FK → projects.id;PK;ON DELETE CASCADE(删项目级联删配置)
--   build_json        : 构建配置 JSON,形如
--                       {"model":"dockerfile|toolchain","dockerfilePath":"...",
--                        "toolchain":{"language":"...","version":"..."},
--                        "artifactType":"image|jar|dist",
--                        "vars":[{"id","key","secret",("value"|"credentialId")}],
--                        "cache":{"enabled":bool,"paths":[...]}}
--   environments_json : 环境定义 JSON 数组,元素
--                       {"id","name","targetServerIds":[...],
--                        "envVars":[{"id","key","secret",("value"|"credentialId")}],
--                        "imageRegistry":{"type":"harbor|acr|dockerhub|custom","url","credentialId"}}

CREATE TABLE IF NOT EXISTS pipeline_settings (
    project_id        TEXT PRIMARY KEY,
    build_json        TEXT NOT NULL DEFAULT '{}',
    environments_json TEXT NOT NULL DEFAULT '[]',
    created_at        TEXT NOT NULL,
    updated_at        TEXT NOT NULL,
    FOREIGN KEY (project_id) REFERENCES projects (id) ON DELETE CASCADE
);
