-- 0045 proxy_routes.config:域名运营增强(R2)的「高级配置」JSON 列。
-- 单列 JSON(而非宽迁移):承载多域名别名(aliases)、访问控制(basic auth / IP 白名单·黑名单)、
-- 安全头与压缩(forceHttps/hsts/securityHeaders/compression)、自定义重定向(redirects)等进阶能力。
-- 刻意存成 JSON,使后续版本能 schema-free 扩展(加字段不再加列、不再加迁移)。
-- 应用层始终写入合法 JSON(或空串=零值配置);basic auth 的 bcrypt 哈希存于此 JSON,但 API DTO
-- 经独立序列化剥离,绝不外泄给前端。R1 老路由 config='' 视作零值配置,行为不变。
ALTER TABLE proxy_routes ADD COLUMN config TEXT NOT NULL DEFAULT '';
