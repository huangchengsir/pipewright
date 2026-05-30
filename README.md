<div align="center">

# Pipewright

**一个轻量、自托管、带 AI 的 CI/CD + 部署编排平台。**
单个 Go 静态二进制(≤100MB),内嵌前端,无外部依赖。
一个工具替掉「CI + Ansible/Kamal + Portainer」三件套。

</div>

---

## 为什么是 Pipewright

主流方案要么重(Jenkins 一堆插件 + JVM),要么是三个工具拼装(Woodpecker/Drone + Ansible/Kamal + Portainer)。Pipewright 把这三件事装进**一个静态二进制**,再加一件别人没有的:**AI 失败诊断**。

| | Pipewright | Jenkins | Drone + Ansible + Portainer |
|---|:---:|:---:|:---:|
| 单二进制部署 | ✅ ≤100MB | ❌ JVM + 插件 | ❌ 三套拼装 |
| 隔离构建 | ✅ | ✅ | ✅ |
| 多服务器部署(SSH) | ✅ 内置 | 插件 | Ansible |
| 服务器/服务运维 | ✅ 内置 | ❌ | Portainer |
| **AI 失败诊断** | ✅ **王牌** | ❌ | ❌ |
| 零停机 + 回滚 | ✅ | 插件 | 自己写 |

**核心理念**:CI/CD 路径绝不依赖 AI —— AI 不可用时优雅降级,核心构建/部署照常。AI 是加分项,不是单点。

## 王牌:AI 失败诊断

构建/部署失败时,Pipewright 不只给你一堆红色日志:

- **根因假说 + 修复建议 + 置信度** —— 语言是「最可能的根因是 X(假说,非结论)」,不虚假确定。
- **「AI 认为」与「原始日志证据」分栏** —— 命中行高亮,假说与证据在同一屏对齐。
- **成功/失败 diff** —— 自动对比「上次成功 vs 本次失败」改了什么(这是「把日志粘进 ChatGPT」给不了的上下文)。
- **反馈闭环** —— 👍/👎 + 正确根因,沉淀成私有诊断知识库,越用越准。
- **出网前脱敏** —— 发往 LLM 的日志先过 secret masking,密钥绝不外泄。
- **自带 LLM 抽象** —— Claude / OpenAI / 本地 Ollama 可配,密钥加密入库,绝不写死。

## 能力总览

- **🔐 安全地基** —— 单管理员认证(argon2id + CSRF)· 凭据加密保险库(NaCl secretbox,掩码呈现,绝无明文)· append-only 审计 · 全链 secret 脱敏。
- **🧩 项目与流水线** —— 可视化流水线编排画布 · 触发规则 + 分支→环境映射 · 构建/部署配置 · **AI 生成流水线配置** · 服务端权威合法性校验。
- **🏗 隔离构建与产物** —— 版本钉死的隔离构建 · 多类型产物(镜像/JAR/dist)契约 · 实时纯黑终端日志(SSE)+ 历史回放 · 只读代码浏览(Monaco)。
- **🚀 多服务器部署** —— 经 SSH agentless 部署 · 健康门控 · 零停机端口切换 + 失败回滚 · 多机并行扇出 + 部分失败可见。
- **📣 通知** —— 企业微信 / 钉钉 / 飞书 / 邮件 / 自定义 webhook · 事件→渠道细粒度路由 · 模板 + 变量自定义。
- **🖥 服务器运维** —— 多机状态总览(CPU/内存/磁盘)· 实时 + 历史服务日志 · 服务操作 · 容器交互终端 · 异常检测告警。
- **🧠 AI 洞察** —— 见上「王牌」。

> 安全不可妥协:凭据仅以密文存储、命令 array 化防注入(AC-SEC-02)、出网 SSRF 收口、日志脱敏。

## 快速开始

```bash
# 1. 构建(纯 Go,无 CGO;前端经 go:embed 内嵌)
make build          # 产出单个静态二进制 ./pipewright

# 2. 运行(首次启动引导管理员;master key 用于凭据保险库)
PIPEWRIGHT_MASTER_KEY=$(head -c32 /dev/urandom | base64) \
PIPEWRIGHT_ADMIN_PASSWORD=change-me \
PIPEWRIGHT_ADDR=:8080 \
./pipewright

# 3. 打开 http://localhost:8080,用 admin / change-me 登录
```

容器运行:

```bash
docker build -t pipewright .
docker run -p 8080:8080 -e PIPEWRIGHT_MASTER_KEY=... -e PIPEWRIGHT_ADMIN_PASSWORD=... pipewright
```

### 配置(环境变量)

| 变量 | 说明 | 默认 |
|---|---|---|
| `PIPEWRIGHT_ADDR` | HTTP 监听地址 | `:8080` |
| `PIPEWRIGHT_DB` | SQLite 数据库路径 | `pipewright.db` |
| `PIPEWRIGHT_MASTER_KEY` | 凭据保险库主密钥(base64 的 32 字节);或用 `_FILE` 指文件 | 未配则保险库禁用 |
| `PIPEWRIGHT_ADMIN_USERNAME` | 首次启动管理员用户名 | `admin` |
| `PIPEWRIGHT_ADMIN_PASSWORD` | 首次启动管理员口令 | 无(须设置) |

## 技术栈

- **后端**:Go · Chi(路由)· modernc/sqlite(纯 Go,无 CGO)· go-git · NaCl secretbox(保险库)· argon2id · golang.org/x/crypto/ssh(agentless 部署)
- **前端**:Vue 3 `<script setup>` · Vite · naive-ui · OKLCH 双主题 · Monaco(只读代码浏览)· 经 `go:embed` 内嵌进二进制
- **AI**:现成 LLM(Claude/OpenAI/Ollama)+ 精选错误知识库,不自训模型

## 架构

```
单静态二进制 (cmd/pipewright)
├── internal/auth        认证 + 会话 + CSRF
├── internal/vault       凭据加密保险库(secretbox)
├── internal/audit       append-only 审计 + 脱敏
├── internal/project     项目接入 + 仓库探测
├── internal/pipeline    流水线 spec + 构建/部署配置 + 校验
├── internal/trigger     webhook + 分支映射触发
├── internal/run         运行模型 + worker pool + 日志 + 产物 + 诊断反馈
├── internal/target      通用 SSH exec/session 层(部署 + 运维共享)
├── internal/deploy      SSH 部署执行 + 健康门控
├── internal/notify      多渠道通知 + 事件路由 + 模板
├── internal/ai          LLM 抽象 + 失败诊断 + 成败 diff + 仓库分析
├── internal/httpapi     唯一对外 HTTP 面(领域包不碰 HTTP)
└── web/                 Vue3 前端(go:embed 内嵌)
```

## 开发状态

🚧 **积极开发中**(借多 AI agent 并行实现)。Epic 1(安全地基)与 Epic 2(项目/流水线配置)已完成;隔离构建、SSH 部署、通知、运维、AI 洞察各 epic 多数已落地;部分依赖 Docker 的隔离构建仍在推进。

## License

待定。

---

<div align="center">
<sub>Pipewright —— 让 CI/CD 的失败可被解释。</sub>
</div>
