# Kubernetes 发布套件（v2） / Kubernetes release suite

v2 将镜像更新、YAML、Helm 和分批发布放在现有 Kubernetes 节点中。原有未声明版本的节点继续按 v1 执行；[原部署指南](kubernetes-deploy.md)保留 v1 的设置与历史行为。

## 先准备连接和执行器

1. 在设置的 Kubernetes 页面保存集群凭据、连接和固定执行器。凭据只在设置中录入；kubeconfig 使用内嵌认证和 CA，不运行 exec/auth-provider 插件、不读取外部证书文件，也不跳过 TLS 校验。
2. 本地 Docker 指 **Pipewright 所在 Linux 主机的 Docker**；SSH 指明确选择的服务器上的 Docker。集群 API 必须从该执行器可达，集群证书也必须匹配。浏览器所在电脑不承担部署。
3. 在连接的发布配置中启用 v2 工具配置：`kube-gateway-v2`、不可变 `repository@sha256:…` 工具镜像、对应架构、与工具镜像一致的非 root UID（示例为 1000），以及固定 helper/kubectl/Helm 路径。v1 配置和授权不会自动变成 v2 授权。
4. 为项目、环境、namespace、具体资源名和执行器建立精确的发布授权。常规读写、关联 Pod/ReplicaSet/EndpointSlice 读取、探测、Helm 存储和集群维护是分别授权的能力。现有集群身份仍需具备 Kubernetes 原生权限；Pipewright 授权不会替你修改 RBAC。

执行前会核验工具和凭据隔离条件。活动 swap、管道式 core dump 或不匹配的运行身份会阻止发送凭据；平台不自动修改宿主安全设置。

## 准备 v2 工具镜像

工具镜像由该版本源码的 `cmd/pipewright-kube-tool`、经官方校验的 kubectl 和 **Helm v4.3.1** 组成。构建文件为 [Dockerfile](../build/kubernetes-tool/Dockerfile)，协议与受限运行要求见[工具说明](kubernetes-release-protocol.md)。使用你的既有镜像仓库；仓库必须能被选定执行器访问。

下面是 Linux/amd64 构建示例。先设置 `PW_TOOL_CONTEXT` 为仓库外的空构建目录，将已独立核对官方摘要的同架构 `kubectl`、`helm` 放入其中；设置 `PW_TOOL_BASE` 为已认可的基础镜像 digest，`PW_TOOL_TAG` 为既有仓库中的工具镜像标签。计算本地文件摘要只验证构建输入一致性，不能替代上一步的官方来源校验。

```sh
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='-s -w' \
  -o "$PW_TOOL_CONTEXT/pipewright-kube-tool" ./cmd/pipewright-kube-tool

docker build -f build/kubernetes-tool/Dockerfile \
  --build-arg BASE_IMAGE="$PW_TOOL_BASE" \
  --build-arg HELPER_SHA256="$(sha256sum "$PW_TOOL_CONTEXT/pipewright-kube-tool" | cut -d ' ' -f 1)" \
  --build-arg KUBECTL_SHA256="$(sha256sum "$PW_TOOL_CONTEXT/kubectl" | cut -d ' ' -f 1)" \
  --build-arg HELM_SHA256="$(sha256sum "$PW_TOOL_CONTEXT/helm" | cut -d ' ' -f 1)" \
  -t "$PW_TOOL_TAG" "$PW_TOOL_CONTEXT"
docker push "$PW_TOOL_TAG"
docker image inspect --format '{{join .RepoDigests "\n"}}' "$PW_TOOL_TAG"
```

把核验过的完整仓库 digest 填入发布配置，不填可变标签或单独的本地 image ID。其他架构需要分别核对三个二进制和 Helm 官方摘要。启用 Helm hooks 的工具还必须明确报告当前 hook 传输和重同步能力；缺失时会在业务写入前拒绝执行。

## 在流水线中选择发布方式

| 方式 | 需要配置 | 完结条件 |
| --- | --- | --- |
| 镜像更新 | 已有 Deployment/StatefulSet/DaemonSet、容器名、已推送或外部固定 digest 镜像来源 | 本次对象、Pod 和容器就绪，镜像身份匹配 |
| YAML | 仓库固定 commit 的文件/目录、节点内 YAML，或冻结的上游制品 | 所有资源应用结果与必需健康检查通过 |
| Helm | 冻结 Chart、values、release 名称；按需开启获准 Job/Pod hooks | 实际 revision、资源回执及获准 hook 完成，不以进程退出码代替 |
| 分批发布 | 合格的 Deployment、Service、候选名称与副本批次 | 每批校验后持久暂停，显式继续或另行回退 |

YAML 的三种来源共用变量替换、预览和预检链。先取代码的节点与制品来源必须能够提供本次冻结输入。变量按声明的类型传入；字符串中的 `${MESSAGE}` 使用已声明变量，`$${HOSTNAME}` 会得到字面 `${HOSTNAME}`，留给容器运行时处理，不再进行第二次替换。缺少真正的变量绑定仍会拒绝执行。预览、审批和运行绑定冻结后的输入；改配置后应发起新的完整运行。

例如，在节点 YAML 中使用：

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: example-config
  namespace: app
data:
  message: ${MESSAGE}
```

再声明 `MESSAGE` 字符串变量并指定固定值或参数来源。Secret 内容和固定敏感输入保存后以保留标记回读；这些标记用于原字段保留，不要复制到其他字段或项目。删除或调整探测顺序会保留未改动探测的私密输入；更换其目标身份需要重新提供相应输入。

## 资源与健康检查

可配置工作负载、Job/CronJob、Service/Ingress、ConfigMap/Secret、PVC/PV/StorageClass、RBAC、HPA/PDB、资源配额、NetworkPolicy、CRD/自定义资源以及 CoreDNS。具体 GVK、scope、原生权限、就绪条件和安全限制以目标集群发现结果和预览为准。声明应用成功不等于资源已提供服务：CronJob 配置需要显式一次 Job 检查才能证明执行，Ingress 路由需显式 HTTP 探测，PVC 的 Bound 与存储 marker 实际读写分别核验；RBAC、配额等对象报告声明结果，自定义资源的实际就绪需配置可核验条件。Ingress 控制器、存储供应器和自定义控制器需要由集群提供。

发布成功要求本次工作负载身份、Pod owner 链、目标容器和所有必需探测通过。HTTP、DNS UDP/TCP、TCP/网络允许、网络拒绝配对和存储探测可按需加入。网络拒绝需要同 namespace、同 IP:port 的成功正向对照；网络拒绝和存储检查不支持非零 `waitSeconds`。

CoreDNS 属于集群维护：候选配置、非抢占候选、原实例 UDP/TCP 查询和精确 Corefile 终结分别核验。它需要明确的维护范围、候选资源和原生权限，不借用应用发布授权。Namespace 安全标签变更、RBAC bind/escalate 等操作同样需要独立能力。

一般数组按声明顺序与长度核对；自定义资源的数组保持原子语义，即使元素有 `name`，或字段恰好叫 `containers`、`env`、`ports`。本套件不会推断任意 CRD 的 map/set 列表语义。只有受支持原生 GVK 的精确 PodSpec 路径和 Service 端口路径使用已知列表键。原生 env/initContainers 的声明顺序会保留；调整已拥有条目时，外部条目的原相对顺序和保留锚点也必须兼容，发生冲突会在写入前拒绝。

### DaemonSet 当前版本与最小就绪时间

新准备的 DaemonSet 计划还需要在该 **具体 DaemonSet** 授权中显式选择“所属 DaemonSet 版本记录读取”（`related-controllerrevisions`），并使用报告 `daemonset-controllerrevision-read-v1` 的本版本工具镜像。旧授权、旧工具和旧计划不会被自动升级。原生身份另需该 namespace 的 `apps/controllerrevisions` 列表读取权限；关联记录在私有网关内按 DaemonSet 的 owner UID 和 namespace 过滤，不提供 namespace 全量公开列表，也不要求 Node 读取。

健康判断先证明 ControllerRevision 的完整模板等于本次已批准并经 admission 的 DaemonSet 模板，再绑定 Pod 的 owner 和当前 revision hash；原生添加或替换的 toleration、目标节点亲和性按该已证明版本核对。自定义 toleration、其他亲和性和容器字段仍需匹配。StatefulSet/DaemonSet 声明正数 `minReadySeconds` 时，还需其原生 available 计数达标，短观察窗口不能替代该条件。

## 失败、取消和回退

- 失败后先看节点日志和逐资源结果。已知失败的本次探测 Job 可按原批准的身份和清理范围移除，失败证据保留；重试使用原节点入口、冻结来源和新的运行审批。
- 已暂停的分批发布可在进程重启后继续。取消会结束本次执行并围栏其后续操作，**不会回滚已发布的批次**；界面与 API 沿用既有失败终态记录取消原因。
- 请求丢失、写入结果未知或清理未确认时保留资源锁，先做只读核对，不自动重发。其他主体同名重建的对象不能沿用旧成功回执。
- 回退创建新的独立审批和运行，只使用可核验的旧状态。部分首次创建失败可回退已确认的创建；未知结果、外部漂移和丢失的已确认对象仍会阻止回退。持久资源或外部 Job 副作用不保证被撤销，以回退预览为准。

保存 YAML 时，已知 `kubectl.kubernetes.io/last-applied-configuration` 注解按不透明私密内容保护，即使 apiVersion/kind 之后才由变量渲染为 Secret。运行保留原始字节；人审使用私密保留标记，按原字段显式保留、替换或删除。此未发布 v2 修复不会自动改写历史数据库；旧公开克隆重新保存时会重新分类，历史计划和哈希保持原值。

## MCP 配置

MCP 与页面使用同一套 v2 配置。先发现能力、生成配置草稿，再由人在配置审阅页检查和批准；应用后可以在节点编辑器回读。模式切换时显式清理旧模式的可选对象；秘密使用已授权引用或由人在设置中录入，不放进对话。配置批准不等同于发布审批，也不会自动执行集群变更。见 [MCP 配置说明](mcp.md)。

## English quick start

The existing node supports v2 image updates, YAML, Helm and progressive release while legacy v1 nodes keep their frozen behavior. Select an explicit local Linux Docker or pinned SSH executor, configure a verified digest-pinned v2 helper image with Helm 4.3.1, and grant exact project/environment/resource capabilities. Git commit files/directories, inline YAML and frozen artifacts share typed variables and preview validation. Workload/Pod/container identity and all required checks decide completion.

Use the Dockerfile and verified binaries described above with an existing accessible registry. Do not assume a mutable tag or local image ID is an immutable tool reference. Hook plans require the current explicit hook/resync capabilities. New DaemonSet plans require an explicit exact-anchor related-controllerrevisions grant, namespace ControllerRevision list permission, and the daemonset-controllerrevision-read-v1 helper capability. Current template/revision/owner proof permits only native DaemonSet Pod transformations; no Node read is implied. StatefulSet/DaemonSet minReadySeconds requires native availability as well as Pod readiness. Cluster maintenance, RBAC escalation, Namespace security changes, related reads and probe writes are separate permissions. Credentials remain write-only.

A progressive pause survives restart; continuing is explicit. Cancellation settles the run without rolling back applied batches. Rollback creates a separate approved run. Unknown writes are reconciled read-only and never automatically replayed. MCP drafts follow the same configuration and human-review boundary; see [MCP](mcp.md). The [original guide](kubernetes-deploy.md) remains the v1 reference.
