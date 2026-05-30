# 安全策略 / Security Policy

## 上报漏洞 / Reporting a Vulnerability

**请勿在公开 Issue 中披露安全漏洞。**
**Please do NOT report security vulnerabilities through public Issues.**

请使用以下任一私密渠道 / Use one of these private channels instead:

1. **GitHub Security Advisory**(推荐 / preferred)——
   在仓库 **Security → Advisories → Report a vulnerability** 提交私密报告。
   Go to **Security → Advisories → Report a vulnerability** in this repo.
2. 私信仓库维护者 / Contact the maintainer privately
   (`github.com/huangchengsir`)。

报告请尽量包含 / Please include where possible:
- 受影响的版本 / 组件(commit 或 tag)
- 复现步骤或 PoC
- 影响评估(数据泄露 / 提权 / RCE 等)
- 可能的修复建议

We aim to acknowledge reports within a few days and keep you updated on the fix.
我们会在数日内确认,并同步修复进展。

## 范围提示 / Scope notes

Pipewright 涉及凭据保险库、SSH 部署、webhook 触发,以下尤需关注:
Given Pipewright handles a credential vault, SSH deploys, and webhook triggers, pay special attention to:

- master key / 凭据掩码绕过 / credential masking bypass
- SSH 部署目标的命令注入 / command injection in deploy targets
- webhook 触发的鉴权与防重放 / webhook auth & replay protection
- 审计日志中的敏感信息泄露 / secret leakage in audit logs

## 安全设计铁律 / Security design rule

CI/CD 关键路径不依赖 AI;AI 失败诊断为旁路,绝不引入对核心构建/部署流程的单点信任。
The CI/CD critical path never depends on AI; AI failure-diagnosis is a side channel and must never become a single point of trust in core build/deploy flows.
