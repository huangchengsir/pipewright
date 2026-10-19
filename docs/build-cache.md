# 构建依赖缓存

在流水线的“变量与缓存”中启用缓存，并填写相对**源码工作区根目录**的路径。
节点未声明 `cachePaths` 时继承该默认值；节点显式设置路径时覆盖默认值，显式空值禁用该节点缓存。
关闭全局默认值不会禁用节点显式配置。`PIPEWRIGHT_NO_BUILD_CACHE=1` 则关闭缓存库和自动 Maven 配置。

## Maven

推荐缓存路径：

```text
.m2/repository
```

也支持 `.m2`；安全的相对路径形式（例如 `./.m2/repository/`）具有相同含义。
路径使用 `/` 分隔。只有有效配置包含 `.m2` 或 `.m2/repository`，且构建器注入了缓存库时，才自动设置 Maven 仓库默认值：

| 执行方式 | 自动仓库位置 |
| --- | --- |
| 脚本节点，包括分组和 DAG 执行 | `/workspace/.m2/repository` |
| 工具链 JAR 构建 | `/src/.m2/repository` |

脚本即使设置 `workDir: backend`，仓库位置仍相对工作区根，而不是 `backend/.m2`。
例如 `mvn -B -DskipTests package` 会把依赖写入恢复和保存使用的同一工作区目录。
本机绑定挂载与 Compose/Docker 的工作区复制模式均通过该目录传递依赖字节；不挂载宿主机 Maven 主目录。

默认值通过容器内的小型 shell 包装器加入 `MAVEN_OPTS`，保留镜像及流水线原有 JVM 选项。
不修改 `HOME`、源码、POM 或 `settings.xml`，也不额外注入镜像下载或网络请求。
没有适用路径、缓存被关闭或节点显式空值时，原执行命令保持不变。

### 显式仓库优先

- 已有 `MAVEN_OPTS` 或 `MAVEN_ARGS` 包含 `maven.repo.local` 时，不注入自动默认值；其他选项和值保持不变。
- 当前工作目录向上的最近 `.mvn/jvm.config` 声明仓库时，也不覆盖它；设置 `MAVEN_BASEDIR` 时以该目录为准。任意切换目录的复杂脚本或 `mvn -f` 指向其他项目时，建议显式设置仓库选项并匹配缓存路径。
- 命令行 `-Dmaven.repo.local=...` 优先于自动注入的 JVM 默认值。
- `MAVEN_ARGS` 需要 Maven 3.9.0 或以上；详见 [Maven 配置说明](https://maven.apache.org/configure.html)。
- 使用自定义仓库时，同时配置对应工作区缓存路径。例如脚本使用 `-Dmaven.repo.local=/workspace/deps/maven`，缓存路径填写 `deps/maven`；JAR 工具链对应 `/src/deps/maven`。工作区之外的目录不会被本缓存持久化。

用户已有镜像或设置中的仓库位置可能被这个自动默认值覆盖；需要保留自定义位置时，显式配置上述选项。
推荐仅缓存 `.m2/repository`，不把凭据或 `settings.xml` 纳入缓存。
Maven 原生默认位置见 [本地仓库配置](https://maven.apache.org/guides/mini/guide-configuring-maven.html)。

## 命中与保存

缓存键由分支、已发现的锁文件/POM 内容摘要及可选节点 `cacheKey` 推导。
构建前恢复，命令成功后保存；失败或取消不保存，缓存错误不改变构建结果。

只有成功恢复了非空普通文件内容，才记录暖缓存。目录、零长度文件、仅符号链接或旧空归档均不算有效命中；
已有工作区文件不会被用来冒充恢复内容。损坏或无法读取的归档按冷构建继续。
gzip 完整性检查只接受单个压缩成员和有界 tar 零填充，尾部读取也响应取消。
没有有效文件字节时不写新归档、不覆盖旧缓存，并记录跳过保存，而不是“已保存”。
打包时不跟随符号链接查找有效内容。

缓存不是依赖完整性的保证。某次缓存命中仍可能缺少当前构建需要的依赖；离线可用性应通过干净工作区的第二次 `mvn -o ...` 验证。

## Dockerfile 限制

上述设置仅用于脚本容器和工具链 JAR 构建，不改变 `docker build` 或 Dockerfile 中的 Maven 命令。
Dockerfile 内的依赖缓存需要自行配置镜像层或 BuildKit 缓存，不能根据本功能的日志推断其命中。
