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
- **🔄 检查 + 一键自更新** —— 设置→系统 显示当前版本,一键查 GitHub 最新发布并语义比对;二进制部署可页面**一键自动更新**(下载新版 + 校验和核验 + 原子替换 + 自重启),Docker 部署给出精确升级命令。
- **🧠 AI 洞察** —— 见上「王牌」。

> 安全不可妥协:凭据仅以密文存储、命令 array 化防注入(AC-SEC-02)、出网 SSRF 收口、日志脱敏。

## 安装 / 部署

三种形态任选,数据均落本地、无外部依赖。

### ① 一键脚本(Linux / macOS)

从 GitHub Release 下载对应平台的静态二进制装到 `/usr/local/bin`(含校验和核验):

```bash
curl -fsSL https://raw.githubusercontent.com/huangchengsir/pipewright/master/install.sh | sh

# 钉版本 / 自定义目录:
VERSION=v1.0.0 INSTALL_DIR=$HOME/.local/bin \
  sh -c "$(curl -fsSL https://raw.githubusercontent.com/huangchengsir/pipewright/master/install.sh)"

# 运行(首次启动引导管理员;master key 用于凭据保险库)
PIPEWRIGHT_MASTER_KEY=$(openssl rand -base64 32) \
PIPEWRIGHT_ADMIN_PASSWORD=change-me \
  pipewright          # 打开 http://localhost:8080,用 admin / change-me 登录
```

> Windows 用户:到 [Releases](https://github.com/huangchengsir/pipewright/releases) 下载 `.zip`。

### ② docker compose(推荐自托管)

```bash
curl -fsSLO https://raw.githubusercontent.com/huangchengsir/pipewright/master/docker-compose.yml
curl -fsSLO https://raw.githubusercontent.com/huangchengsir/pipewright/master/.env.example
cp .env.example .env       # 至少设 PIPEWRIGHT_ADMIN_PASSWORD,并 openssl rand -base64 32 填 MASTER_KEY
docker compose up -d       # 数据持久化在具名卷 pipewright-data;切 MySQL 见 .env 注释
```

### ③ docker run(最快试用)

```bash
docker run -d -p 8080:8080 -v pipewright-data:/data \
  -e PIPEWRIGHT_ADMIN_PASSWORD=change-me \
  -e PIPEWRIGHT_MASTER_KEY=$(openssl rand -base64 32) \
  ghcr.io/huangchengsir/pipewright:latest
```

### 从源码构建

```bash
make build          # 前端构建 → go:embed → 单个静态二进制 ./pipewright(纯 Go,无 CGO)
./pipewright --version
```

### 更新

打开 **设置 → 系统**,点「检查更新」查最新发布;有新版时:

- **二进制部署**:点「立即更新」即自动下载新版 + 校验和核验 + 替换 + 重启(需对二进制文件有写权限;装在 `$HOME/.local/bin` 免 sudo)。
- **Docker 部署**:容器不替换自身镜像,按提示在宿主执行 `docker compose pull && docker compose up -d`(数据卷保留)。

### 配置(环境变量)

| 变量 | 说明 | 默认 |
|---|---|---|
| `PIPEWRIGHT_ADDR` | HTTP 监听地址 | `:8080` |
| `PIPEWRIGHT_RELEASE_REPO` | 检查更新所查的 GitHub 仓库(fork 可改) | `huangchengsir/pipewright` |
| `PIPEWRIGHT_DB_DRIVER` | 数据库驱动:`sqlite` 或 `mysql` | `sqlite` |
| `PIPEWRIGHT_DB` | SQLite 数据库路径(driver=sqlite 时) | `pipewright.db` |
| `PIPEWRIGHT_DB_DSN` | MySQL DSN(driver=mysql 时必填) | 无 |
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

## 贡献 / Contributing

欢迎 PR 与 Issue!动手前请读 [CONTRIBUTING.md](CONTRIBUTING.md)(搭环境 / 测试 / 提交规范),并遵守[行为准则](CODE_OF_CONDUCT.md)。安全漏洞请走私密渠道,见 [SECURITY.md](SECURITY.md)。

## License

MIT — 见 [LICENSE](LICENSE)。

---

<div align="center">
<sub>Pipewright —— 让 CI/CD 的失败可被解释。</sub>
</div>
