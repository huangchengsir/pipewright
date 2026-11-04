# Kubernetes 部署

Kubernetes 节点将本次流水线已推送的镜像更新到已有 Deployment 的指定容器，并等待这次更新就绪。执行位置可选 Pipewright 所在主机的 Docker，或已登记并核实主机密钥的 SSH 服务器。

## 配置顺序

1. 在“设置 → 安全与账户 → 凭据保险库”创建 Kubernetes 认证。可以导入内嵌认证的 kubeconfig 并明确选择 context，或填写 API Server、CA 和 Token。此类认证单独加密保存，仅用于受控 Kubernetes 节点；保存后不提供明文查看。轮换需要重新提供完整认证材料，启停不改变原材料。
2. 在“设置 → 资源与发布 → Kubernetes”创建连接，选择认证、受信任的 kubectl 工具镜像和允许的执行位置。SSH 执行位置需要已经核实的 SHA-256 主机密钥指纹。
3. 为连接添加精确授权：项目、环境、namespace、Deployment、允许的容器和执行位置。默认要求人工审批。可对一个明确授权的目标测试连接；此操作只读取目标，不发布。
4. 在流水线镜像推送阶段之后添加 Kubernetes 部署节点，选择上述目标和明确的上游 push_image 来源。该节点独占一个部署阶段，超时可设置为 30–1800 秒，默认 300 秒。
5. 启动运行。审批绑定本次推送的不可变镜像与目标；管理员授权要求审批时，仓库配置不能关闭这道审批。

配置变更、测试连接、核对结果和结束跟踪需要最近五分钟内的密码确认。并发修改发生版本冲突时，重新读取并审阅现有配置后再保存。

## 执行环境

工具使用 Linux Docker Engine，不依赖宿主安装 kubectl。工具镜像必须按 repository@sha256 固定，包含 /bin/sh 和配置的 kubectl 绝对路径，支持指定架构及非 root UID。kubectl 与 API Server 的次版本差不得超过 1；执行时会重新核对实际服务端版本。私有工具镜像可选已有镜像仓库认证。

工具容器使用只读根文件系统、私有 tmpfs、固定 bridge 网络和受限资源，认证通过标准输入送入临时文件系统。当前环境验证要求没有活动 swap，也不能使用管道式 core dump。条件不满足会在传入认证之前拒绝执行；Pipewright 不会自动修改宿主策略。

kubeconfig 只接受内嵌的认证与 CA，不执行 exec/auth-provider 插件，不读取外部证书、token 文件或代理配置，也不跳过 TLS 校验。首版仅更新已有、未暂停且副本数大于零的 Deployment，不执行任意 kubectl 命令、Helm 或 YAML apply。集群身份需要对指定 namespace 下的 Deployment 具有 get、patch 权限。实际需要更新镜像时，节点还会通过 `authorization.k8s.io/v1` 的 `SelfSubjectAccessReview` 核对当前身份对该目标的 patch 权限；此授权检查不保存集群资源，不会修改 RBAC。Kubernetes 默认的 `system:basic-user` 角色通常允许已认证身份创建这种权限检查；定制集群需由管理员确认对应权限。

权限检查明确拒绝时，运行在发送镜像更新前失败；检查服务不可用或响应无法验证时也停止发布，并提示“无法确认发布权限”。Pipewright 不会自动增加权限。检查允许后仍可能发生权限或集群状态变化：实际更新请求已经发出而结果不明时，仍显示“结果待核对”，不能据先前权限检查或错误文字认定请求从未提交。

## 查看结果与重试

节点保持简要状态，详细步骤和错误在原“节点日志”弹窗内查看。业务结果与工具清理结果分别展示；集群已就绪但临时工具清理未核实，仍不算完整成功。

- **已就绪**：目标身份、镜像、此次更新标记和副本就绪条件一致。
- **无需更新**：已运行同一镜像，并且当前目标健康；不会隐式重启 Deployment。
- **已被其他发布替换**：目标被删除重建，或其模板、镜像、更新归属发生变化。
- **结果待核对**：请求可能已被集群接受，但响应不足以确认。使用只读核对获得当前事实，不会重复提交更新。
- **清理待核实**：临时执行资源尚未能证明已退出并移除，相关重试继续受阻。

只有本次运行收尾后才可重试。已成功节点在复用前仍需核验当前目标；后来的发布已经替换它时，不采用过期成功。原失败节点后来经核对为就绪、且清理已确认，可以在新尝试中采用该事实继续后继，原运行仍保留失败历史。

“结束跟踪”只在严格前提满足时释放 Pipewright 对这次操作的本地占用；它不会撤销集群更新，也不会把未知结果改成成功。Kubernetes 历史不使用 SSH 一键回滚。环境活动版本仅在整个运行和所有必要目标成功、清理已核实时推进。

## 重启与数据目录

`PIPEWRIGHT_DATA_DIR` 可指定持久数据目录，默认 `.pipewright`。Kubernetes 控制器在其中保存进程身份记录并持有操作系统锁，用于重启后核实旧进程已退出。保持目录位置和权限稳定；不要通过删除、替换锁文件或身份记录来绕过尚未核实的操作。备份恢复或迁移后不能仅凭复制的记录证明原进程已退出。

## English quick start

Create a protected Kubernetes credential under Settings → Security & account → Credential vault. Then add a connection under Resources & delivery → Kubernetes, select an immutable trusted tool image, and grant an exact project/environment/namespace/Deployment/container/executor scope. SSH executors require a verified host-key fingerprint. Add the Kubernetes job in a dedicated deployment stage after an explicit `push_image` ancestor. Approval is required by default.

The node updates one existing Deployment container to this run’s pushed repository digest and waits for that deployment’s identity and rollout. It supports local Linux Docker and pinned SSH Docker; it does not run arbitrary kubectl, Helm or apply commands. The tool container needs a shell, compatible kubectl, a non-root UID and a fixed repository digest. Active swap or piped core-dump handling fails the credential-isolation preflight before secrets are sent. The application never changes host policy automatically.

Business outcome and tool cleanup are separate. An unknown result must be reconciled read-only; never resend the update to infer success. A later verified result can be adopted by an explicit retry after the original run settles, retaining its original failure history. Retiring tracking releases only a verified local hold, without undoing Kubernetes changes. Kubernetes deployment history does not use SSH rollback. Keep `PIPEWRIGHT_DATA_DIR` persistent and do not delete its process-identity journal to bypass unresolved work.

Before changing an image, the node submits a fixed, nonpersistent `SelfSubjectAccessReview` for `patch` on the exact Deployment. The identity therefore needs access to this authorization review API in addition to scoped Deployment `get` and `patch` permissions; Kubernetes normally supplies review access through `system:basic-user`, but custom RBAC must be checked by the administrator. A denied or unverifiable review stops the update before dispatch. Pipewright never changes RBAC automatically. Permission may change after this check, so an uncertain result from an actual PATCH still requires read-only reconciliation and is never automatically replayed.
