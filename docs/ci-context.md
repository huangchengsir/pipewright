# 流水线运行与 Git 上下文

执行图符合下述自动 Git 上下文条件时，源码阶段的 `git_source` 确认实际检出的提交，并将运行信息和 Git 元数据提供给依赖该阶段的脚本节点。
在脚本的 `commands` 中使用 Shell 环境变量即可读取，无需在流水线变量中手动配置。

## 可用变量

| 变量 | 含义 |
| --- | --- |
| `PIPELINE_ID` | 当前流水线运行 ID。节点重试创建新运行时使用新 ID。 |
| `DATETIME` | 当前运行的创建时间，使用 UTC，格式为 `yyyy-MM-dd HH:mm:ss`。 |
| `TIMESTAMP` | 与 `DATETIME` 对应的 Unix 秒级时间戳。 |
| `CI_COMMIT_TITLE` | 实际检出提交的提交信息首行。 |
| `CI_COMMIT_ID` | 实际提交的完整 SHA 前 7 位。 |
| `CI_COMMIT_SOURCE` | 项目源码地址，移除用户信息、查询参数和片段。 |
| `CI_COMMIT_REF_NAME` | 本次运行选择的分支或引用；未指定时使用项目默认分支，两者均为空时该值为空。 |
| `CI_COMMIT_AUTHOR` | 实际检出提交的作者名称，不包含邮箱。 |
| `CI_COMMIT_SHA` | 实际检出提交的完整 SHA。 |

`CI_COMMIT_ID` 是便于展示的短标识，不保证全局唯一。需要精确识别提交时使用 `CI_COMMIT_SHA`。
`CI_COMMIT_SHORT_SHA` 不作为系统变量自动注入，短 SHA 请使用 `CI_COMMIT_ID`。

## 在流水线中使用

在真实 DAG 流水线中设置一个源码阶段，并让使用变量的阶段直接或间接依赖它：

```yaml
version: 1
stages:
  - id: stg_src
    name: 流水线源
    kind: source
    jobs:
      - id: job_src
        name: 代码源
        type: git_source

  - id: stg_build
    name: 构建
    kind: build
    needs: [stg_src]
    jobs:
      - id: job_build
        name: 输出运行上下文
        type: script
        script:
          image: alpine:3
          commands:
            - |
              printf 'run=%s time=%s timestamp=%s\n' "$PIPELINE_ID" "$DATETIME" "$TIMESTAMP"
              printf 'source=%s ref=%s sha=%s id=%s\n' "$CI_COMMIT_SOURCE" "$CI_COMMIT_REF_NAME" "$CI_COMMIT_SHA" "$CI_COMMIT_ID"
              printf 'title=%s author=%s\n' "$CI_COMMIT_TITLE" "$CI_COMMIT_AUTHOR"
```

变量由脚本容器中的 Shell 展开；提交标题、作者和地址可能包含空格或特殊字符，引用时应加双引号。
示例会将这些值写入运行日志，使用前请确认仓库地址和提交信息适合公开给日志查看者。

系统变量由平台注入，运行参数、流水线变量、环境变量或上游输出中的同名值不能覆盖它们。
节点的 `script.env` 当前虽可解析，但脚本执行器尚未读取该配置，不应依赖它注入变量。

## 源码阶段与重试

- 自动 Git 上下文的资格在执行前按展开后的实际图判断，节点重试使用冻结阶段和冻结依赖：有且仅有一个 `source` 阶段，且该阶段只包含一个 `git_source`；源码阶段无依赖、审批门、允许失败、旁挂服务或后置步骤。
- 自动 Git 上下文还要求其他阶段均直接或间接依赖源码阶段。所有阶段都未配置 `needs` 时，沿用阶段声明顺序的依赖；已有显式或冻结依赖保持原样。
- 源码阶段确认并绑定实际提交后，下游工作区按同一个 SHA 检出并校验。解析、持久化、元数据、检出或校验失败时阻止后继执行，不在运行中转入兼容模式。
- 同一次运行中，运行 ID、创建时间和 Git 上下文保持一致。节点重试继承原运行绑定的 SHA，按该 SHA 读取提交元数据，而不改用分支最新提交；新运行的 ID 和创建时间会变化。若原提交无法读取，重试会失败。

## Git 上下文兼容模式

不符合上述资格的图继续使用原 DAG 执行语义，包括源码阶段审批、post、旁挂服务、允许失败、多个 job、源码矩阵、前置依赖和独立分支。平台不改写阶段或依赖，也不切换到 legacy 固定流程。在执行计划记录成功后，仅写入一次兼容提示。

兼容模式不安装自动 Git 上下文，因此不保证本文九个新增系统变量或统一源码绑定；已有变量、检出、重试、审批和失败传播规则仍按原行为执行。是否采用兼容模式只由执行前的图资格决定。Kubernetes 准备、强制审批及冻结授权快照在两种模式下均保留。

## 适用范围与限制

- 这些变量适用于真实 DAG 执行器中已支持的下游脚本节点及脚本后置步骤；远程 runner 的后置步骤和输出变量回采仍受现有能力限制。旧版 legacy 固定流程不提供此变量契约。
- 变量不是任意 YAML 字段的模板替换，不会自动传入 Dockerfile 构建参数或 SSH 部署目标机。如需传递，须按对应构建或部署方式显式处理。
- `push_image` 的 `${COMMIT_SHA}` 是现有镜像 tag 字段专用模板，不等同于通用 YAML 中的 `${CI_COMMIT_SHA}` 替换。
- 流水线即代码（PAC）当前先按分支读取 `.pipewright.yml`，再由源码节点确认提交。若两次读取之间分支发生变化，不能保证 YAML 与后续检出源码来自同一个提交。
- `CI_COMMIT_SOURCE` 不携带 URL 用户信息、查询参数或片段，但提交标题、作者及仓库路径仍可能包含敏感内容；凭据应通过平台凭据系统管理，不应写入提交信息或普通变量。
