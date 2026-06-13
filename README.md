<div align="center">

# Pipewright

**一个轻量、自托管的 CI/CD + 部署 + 运维一体化平台。**
单个 Go 静态二进制(内嵌前端,运行时零依赖),
一个工具替掉「CI + Ansible/Kamal + Portainer」三件套。

[![Release](https://img.shields.io/github/v/release/huangchengsir/pipewright)](https://github.com/huangchengsir/pipewright/releases)
[![CI](https://github.com/huangchengsir/pipewright/actions/workflows/ci.yml/badge.svg)](https://github.com/huangchengsir/pipewright/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

简体中文 | [English](README.en.md)

</div>

---

## 为什么是 Pipewright

主流方案要么重(Jenkins 一堆插件 + JVM),要么是三个工具拼装(Woodpecker/Drone + Ansible/Kamal + Portainer)。Pipewright 把「持续集成、多服务器部署、服务器/容器运维」装进**一个静态二进制**:下载、启动、打开浏览器,就是全部安装过程。

| | Pipewright | Jenkins | Drone + Ansible + Portainer |
|---|:---:|:---:|:---:|
| 单二进制部署 | ✅ | ❌ JVM + 插件 | ❌ 三套拼装 |
| 可视化流水线编排(DAG) | ✅ 内置画布 | 插件 | YAML 手写 |
| 隔离构建 | ✅ | ✅ | ✅ |
| 多服务器部署(SSH,免 Agent) | ✅ 内置 | 插件 | Ansible |
| 服务器 / 容器运维 | ✅ 内置 | ❌ | Portainer |
| 零停机 + 失败回滚 | ✅ | 插件 | 自己写 |
| 一键自更新 | ✅ | ❌ | ❌ |

## 界面预览

**全局概览** —— 项目、运行成功率、环境部署态、服务器健康、DORA 指标,一屏全局:

![Dashboard](docs/screenshots/dashboard.png)

**可视化流水线编排** —— 阶段/任务两级 DAG 画布:横向连线串行、纵向并排真并行,支持矩阵构建、人工审批门、旁挂服务、阶段后置步骤;画布与 YAML 双向往返:

![流水线编排画布](docs/screenshots/pipeline-canvas.png)

**运行详情** —— 阶段流转、实时日志(SSE 推送 + 历史回放)、构建产物与镜像引用、逐步骤状态:

![运行详情](docs/screenshots/run-detail.png)

**容器管理** —— 跨主机容器/镜像/Stacks/卷/网络一站管理,生命周期操作、实时 stats、日志、交互终端:

![容器管理](docs/screenshots/containers.png)

## 能力总览

- **🔐 安全地基** —— 单管理员认证(argon2id + CSRF)· 凭据加密保险库(NaCl secretbox,掩码呈现,绝无明文)· append-only 审计 · 全链路 secret 脱敏。
- **🧩 项目与流水线** —— 可视化编排画布(阶段 DAG + 阶段内任务级 DAG)· 矩阵构建 · 人工审批门 · 旁挂服务(测试挂 DB/Redis)· 触发规则 + 分支→环境映射 · 服务端权威合法性校验。
- **🏗 隔离构建与产物** —— 版本钉死的容器内隔离构建 · 构建依赖缓存 · 多类型产物(镜像/JAR/dist)+ 推送私有镜像仓库 · 实时终端日志(SSE)+ 历史回放 · 只读代码浏览(Monaco)。
- **🚀 多服务器部署** —— 经 SSH 免 Agent 部署 · 健康门控 · 零停机切换 + 失败回滚 · 多机并行扇出 + 部分失败可见 · 环境部署历史与回滚。
- **📣 通知** —— 企业微信 / 钉钉 / 飞书 / 邮件 / 自定义 webhook · 事件→渠道细粒度路由 · 模板 + 变量自定义 · 流水线内通知节点。
- **🖥 服务器与容器运维** —— 多机状态总览(CPU/内存/磁盘)· 容器/镜像/Stacks/卷/网络管理 · 实时 + 历史服务日志 · 容器交互终端 · Web 运维终端(主机 shell,完整复制粘贴/信号支持)· 异常检测告警。
- **📈 度量** —— DORA 四指标(部署频率/变更前置时长/变更失败率/平均恢复时长)开箱即用。
- **🔄 检查 + 一键自更新** —— 设置→系统 一键查 GitHub 最新发布并语义比对;二进制部署可页面**一键自动更新**(下载 + 校验和核验 + 原子替换 + 自重启),Docker 部署给出精确升级命令。

> 安全不可妥协:凭据仅以密文存储、命令 array 化防注入、出网 SSRF 收口、日志脱敏。

## 安装 / 部署

三种形态任选,平台本体是单静态二进制、**运行时零依赖**(无需 Go/Node)。

> **Docker 前置**:平台本体不依赖 Docker,但**「隔离构建 / 容器部署」需要 Docker**(没有则降级到桩 runner、不做真实构建)。控制台 / SSH 部署 / 通知不需要。一键脚本会**检测 Docker** 并在缺失时提示;Linux 下可 `INSTALL_DOCKER=1` 自动安装(经官方 get.docker.com),macOS 请装 Docker Desktop。

### ① 一键脚本(Linux / macOS)

从 GitHub Release 下载对应平台的静态二进制装到 `/usr/local/bin`(含校验和核验 + Docker 检测):

```bash
curl -fsSL https://raw.githubusercontent.com/huangchengsir/pipewright/master/install.sh | sh

# 钉版本 / 自定义目录 / Linux 顺带自动装 Docker:
VERSION=v1.0.0 INSTALL_DIR=$HOME/.local/bin INSTALL_DOCKER=1 \
  sh -c "$(curl -fsSL https://raw.githubusercontent.com/huangchengsir/pipewright/master/install.sh)"

# 运行(首次启动引导管理员;master key 用于凭据保险库)
PIPEWRIGHT_MASTER_KEY=$(openssl rand -base64 32) \
PIPEWRIGHT_ADMIN_PASSWORD=change-me \
  pipewright          # 打开 http://localhost:8080,用 admin / change-me 登录
```

**推荐:装为 systemd 服务**(开机自启 + 崩溃重启 + 一键自更新可用;Linux,需 root)。脚本会自动持久化 master key 到 `/etc/pipewright/master.key`、数据落 `/var/lib/pipewright`、配置写 `/etc/pipewright/pipewright.env`:

```bash
SETUP_SERVICE=1 sh -c "$(curl -fsSL https://raw.githubusercontent.com/huangchengsir/pipewright/master/install.sh)"
# 状态 / 日志:systemctl status pipewright  ·  journalctl -u pipewright -f
# 改端口等:编辑 /etc/pipewright/pipewright.env 后 systemctl restart pipewright

# 用 MySQL 而非默认 SQLite(DSN 为 go-sql-driver 格式,parseTime=true 必带):
SETUP_SERVICE=1 PIPEWRIGHT_DB_DRIVER=mysql \
  PIPEWRIGHT_DB_DSN='user:pw@tcp(host:3306)/pipewright?parseTime=true&charset=utf8mb4' \
  sh -c "$(curl -fsSL https://raw.githubusercontent.com/huangchengsir/pipewright/master/install.sh)"
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

- **二进制部署**:点「立即更新」即自动下载新版 + 校验和核验 + 替换 + 重启(需对二进制文件有写权限;装在 `$HOME/.local/bin` 免 sudo,或用 `SETUP_SERVICE=1` 装的 root systemd 服务亦满足)。
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
| `PIPEWRIGHT_RUNNER` | 运行执行器:默认 DAG(按画布 stages/script/deploy_ssh/notify 编排执行);设 `legacy` 回退旧版固定流程 | `dag` |

## 流水线即代码(GitOps)

把流水线结构写进仓库的 `.pipewright.yml`,**跟代码同源、走 PR 评审、按分支演进**——不再依赖画布配置的隐式漂移。

- **开启**:在项目的流水线页打开「流水线即代码 / Pipeline as code」开关(按项目维度)。
- **生效方式**:开启后,每次运行都从**本次构建分支**的**仓库根**读取 `.pipewright.yml`(分支为空时回退项目默认分支),用其中的流水线 spec 驱动本次运行;不同分支可携带各自的 `.pipewright.yml`。文件用项目绑定的仓库凭据临时拉取,无新增暴露面。
- **永不卡住运行的回退**:文件**缺失** → 回退到画布(UI)里已配置的流水线;文件存在但 **YAML 非法** → 同样回退到已存的画布配置。
- **作用范围**:YAML 只管**流水线结构**(阶段 / 任务 / `needs` / DAG 编排);**变量与缓存、环境与凭据、触发规则**仍来自画布(UI)设置,**不写在 YAML 里**。
- **schema** 与平台「从 YAML 导入」用的是同一套(`version` + `stages` → `jobs`,job 用嵌套 `script:` 块写 `image`/`commands`/`env`/`workdir`)。

```yaml
version: 1
stages:
  - id: stg_src              # needs 按阶段 id 引用,故跨阶段依赖须显式写 id
    name: 流水线源
    kind: source
    jobs:
      - name: Gitee 源
        type: git_source
  - id: stg_build
    name: 构建
    kind: build
    needs: [stg_src]
    jobs:
      - name: 运行测试
        type: script
        script:
          image: golang:1.23
          commands:
            - go vet ./...
            - go test ./...
          env:
            CGO_ENABLED: "0"
          workdir: src/app
  - id: stg_deploy
    name: 部署
    kind: deploy
    needs: [stg_build]
    gate: true               # 人工审批门
    when:
      branches: [main, release/*]
    jobs:
      - name: SSH 部署
        type: deploy_ssh
        config:
          targetEnv: prod
```

> 也可用全局环境变量 `PIPEWRIGHT_PAC_RUNTIME=1` 对**所有项目**强制开启流水线即代码(无视各项目开关),供向后兼容 / 高级用户使用。

## 技术栈

- **后端**:Go · Chi(路由)· modernc/sqlite(纯 Go,无 CGO)· go-git · NaCl secretbox(保险库)· argon2id · golang.org/x/crypto/ssh(免 Agent 部署)
- **前端**:Vue 3 `<script setup>` · Vite · naive-ui · OKLCH 双主题 · Monaco(只读代码浏览)· 经 `go:embed` 内嵌进二进制

## 架构

```
单静态二进制 (cmd/pipewright)
├── internal/auth        认证 + 会话 + CSRF
├── internal/vault       凭据加密保险库(secretbox)
├── internal/audit       append-only 审计 + 脱敏
├── internal/project     项目接入 + 仓库探测
├── internal/pipeline    流水线 spec + 构建/部署配置 + 校验
├── internal/trigger     webhook + 分支映射触发
├── internal/run         运行模型 + worker pool + 日志 + 产物
├── internal/dagrun      DAG 调度(阶段级 + 任务级,矩阵展开)
├── internal/build       隔离构建 + 镜像/产物 + 依赖缓存
├── internal/target      通用 SSH exec/session 层(部署 + 运维共享)
├── internal/deploy      SSH 部署执行 + 健康门控 + 回滚
├── internal/notify      多渠道通知 + 事件路由 + 模板
├── internal/httpapi     唯一对外 HTTP 面(领域包不碰 HTTP)
└── web/                 Vue3 前端(go:embed 内嵌)
```

## 开发状态

✅ **已正式发布**,持续迭代中 —— 最新版本见 [Releases](https://github.com/huangchengsir/pipewright/releases)(tag 驱动发版:6 平台二进制 + ghcr 多架构镜像)。已在真实生产环境承载多项目的构建、部署与日常运维。

## 贡献 / Contributing

欢迎 PR 与 Issue!动手前请读 [CONTRIBUTING.md](CONTRIBUTING.md)(搭环境 / 测试 / 提交规范),并遵守[行为准则](CODE_OF_CONDUCT.md)。安全漏洞请走私密渠道,见 [SECURITY.md](SECURITY.md)。

## License

MIT — 见 [LICENSE](LICENSE)。

---

<div align="center">
<sub>Pipewright —— 把 CI、部署与运维装进一个二进制。</sub>
</div>
