export default {
  "repairCredentialDescription": "为这个项目重新选择仓库凭据。只有点击保存才会更新项目。",
  "title": "首次成功运行",
  "intro": "创建项目，准备流水线，然后由你确认运行。",
  "projectLabel": "当前项目",
  "resultProjectLabel": "成功运行所属项目",
  "projectUnavailable": "项目名称暂不可用",
  "stepsLabel": "上手步骤",
  "steps": {
    "project": "创建项目",
    "pipeline": "准备流水线",
    "run": "成功运行一次"
  },
  "stepDescriptions": {
    "project": "连接自己的仓库。当前项目创建需要仓库凭据。",
    "pipeline": "保存适用配置，检查实际任务需要的凭据和执行环境；不要求无关服务器。",
    "run": "在现有确认界面主动提交任务，查看流水线运行结果。"
  },
  "stepStates": {
    "saved": "已保存",
    "done": "已完成",
    "current": "当前步骤",
    "pending": "待进行",
    "unknown": "待确认"
  },
  "states": {
    "loading": "正在读取实例状态",
    "unknown": "暂时无法确认",
    "create": "先创建项目",
    "queued": "运行排队中",
    "running": "流水线运行中",
    "waiting_approval": "等待审批",
    "failed": "查看最近失败",
    "configure": "继续准备流水线",
    "unconfirmed": "核对流水线配置",
    "repository": "核对仓库流水线",
    "ready": "可以前往运行",
    "success": "流水线运行成功",
    "legacy": "已有成功运行记录",
    "stub": "演示运行尚不计入完成",
    "mixed": "混合执行尚不计入完成",
    "pending": "执行来源待确认"
  },
  "descriptions": {
    "loading": "只读取当前实例的数据。你仍可跳过或返回工作台。",
    "unknown": "查询未能确认完整状态，不等于没有项目或成功记录。请重试。",
    "create": "复用项目创建流程，选择仓库凭据并填写默认分支。",
    "queued": "查看所选项目的最新任务；无需再次提交。",
    "running": "查看最新运行的进度与结果。",
    "waiting_approval": "前往运行详情查看审批状态；引导不会自动批准。",
    "failed": "前往该次运行查看日志，修复同一个项目后再运行。",
    "configure": "检查下面的适用缺项。登记服务器不等于连接已验证。",
    "unconfirmed": "当前无法确认配置已就绪。可以核对配置，也可通过原有入口手动运行。",
    "repository": "此项目使用仓库 .pipewright.yml。引导不会拉取仓库，也不要求另存 UI 配置。",
    "ready": "打开该项目的运行确认框。任务只在你提交后执行。",
    "success": "实例已有真实执行的成功流水线记录。这不额外证明部署或服务连通性。",
    "legacy": "历史成功记录满足兼容规则；无法据此额外证明真实构建或部署。",
    "stub": "这次成功包含演示执行，不能证明真实任务已跑通。",
    "mixed": "这次运行同时包含真实与演示任务，不能计为首次真实成功。",
    "pending": "运行已成功，但执行证据仍未确认；不会推断为真实成功。"
  },
  "actions": {
    "create": "创建项目",
    "configure": "继续配置",
    "checkPipeline": "核对流水线",
    "goRun": "前往运行",
    "viewRun": "查看运行",
    "viewFailed": "查看失败日志",
    "edit": "修改流水线",
    "viewResult": "查看运行结果",
    "autoTrigger": "配置自动触发",
    "runtimeHelp": "执行环境帮助",
    "retry": "重试",
    "open": "打开此步骤",
    "servers": "登记服务器",
    "repairCredential": "修改仓库凭据",
    "credentials": "管理凭据"
  },
  "issues": {
    "build": "检查当前构建方式所需的配置和工具链。",
    "tasks": "检查任务类型、镜像与命令；当前执行器可能不支持部分任务。",
    "credentials": "实际使用的凭据引用缺失。",
    "projectCredential": "项目仓库凭据缺失，请在项目中重新选择。",
    "vault": "凭据保险库未配置主密钥，现有凭据不能确认可用。",
    "environment": "当前分支引用的环境尚未定义。",
    "noTasks": "当前分支没有可执行的实际任务。",
    "notification": "实际通知任务引用的通道不存在。",
    "pipeline": "流水线结构需要核对。",
    "runtime": "当前启动的执行器为演示模式。",
    "server": "节点或远程运行器引用的服务器不存在。",
    "storage": "相关配置暂时无法读取，请重试。"
  },
  "runtime": {
    "stub": "当前实际使用演示执行器，不能执行真实任务。",
    "available": "已选用非演示执行器，本机容器 CLI 已被识别；这不是容器服务连通性证明。",
    "unknown": "执行能力或远程连接尚未确认；不会自动探测连接。",
    "instructions": "检查运行 Pipewright 的主机或容器的容器 CLI、服务权限与连接配置。远程运行器在对应项目的变量配置中设置。这里只提供帮助，不安装软件、不测试 SSH，也不提交任务。"
  }
}
