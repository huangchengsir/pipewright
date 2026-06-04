package ai

// 节点目录(AI 生成流水线的「可用工具清单」)。
//
// 痛点:旧 prompt 只举 git_source/build_image/deploy 三种 type,LLM 不知道产品其余节点存在,
// 只会产最粗的三段式。这里把**全部可用节点**作为工具清单喂给 LLM:内置节点(下方 Go 目录,
// 新增模板节点在此登记即自动生效)+ 复用库里的用户自定义节点(运行时从 DB 动态拼入,见
// httpapi 装配)。目录与前端 jobConfigSchema 的节点种类对齐,改动时请同步两侧。

// NodeKind 是一个可用节点的描述(喂给 LLM 的工具条目)。
type NodeKind struct {
	Type        string // job.type(LLM 只能从目录里选)
	Label       string // 人读名
	Category    string // source|build|deploy|quality|notify|custom
	Description string // 用途 + 关键配置/适用场景(给 LLM 判断何时用)
	Custom      bool   // true = 复用库里的用户自定义节点(按 Label 名称选用,type 通常为 templated)
}

// BuiltinNodeCatalog 返回内置节点的工具清单(新增内置/模板节点在此登记即生效)。
// 顺序按典型流水线推进(源 → 构建 → 部署 → 通知),便于 LLM 组合。
func BuiltinNodeCatalog() []NodeKind {
	return []NodeKind{
		{Type: "git_source", Label: "Gitee 源", Category: "source",
			Description: "拉取 Git 仓库源码到构建工作区。每条流水线必须恰有一个 source 阶段,含一个 git_source。"},
		{Type: "build_frontend", Label: "前端构建", Category: "build",
			Description: "Node 容器内装依赖并构建前端,产出 dist。适合含 package.json 的前端/子目录。"},
		{Type: "build_backend", Label: "后端构建", Category: "build",
			Description: "Maven/Gradle 容器内打包后端,产出 jar。适合含 pom.xml / build.gradle 的后端/子目录。"},
		{Type: "build_image", Label: "构建镜像", Category: "build",
			Description: "用 Dockerfile 或工具链构建 Docker 镜像(产物=image)。有 Dockerfile 时优先用它。"},
		{Type: "push_image", Label: "推送镜像", Category: "build",
			Description: "把构建出的镜像推送到镜像仓库。通常紧随 build_image,部署 image 产物前需要。"},
		{Type: "script", Label: "自定义脚本", Category: "build",
			Description: "隔离容器内执行任意命令(跑测试、lint、代码扫描、自定义步骤等)。"},
		{Type: "deploy_ssh", Label: "SSH 部署", Category: "deploy",
			Description: "经 SSH 把产物(jar/dist/image)部署到目标服务器。"},
		{Type: "deploy_frontend", Label: "前端推送部署", Category: "deploy",
			Description: "把前端 dist 经 SSH 零停机部署到服务器(滚动 + reload)。"},
		{Type: "health_check", Label: "健康检查", Category: "deploy",
			Description: "部署后探测服务健康(HTTP/命令),失败可回滚。建议接在部署节点之后。"},
		{Type: "notify", Label: "通知", Category: "notify",
			Description: "运行到此节点时向已配渠道(飞书/Webhook/邮件)发通知,支持标题/正文模板。"},
		{Type: "templated", Label: "自定义节点", Category: "custom",
			Description: "用户自定义节点:参数 + 命令模板({{参数}}),用于产品未内置的步骤。"},
	}
}
