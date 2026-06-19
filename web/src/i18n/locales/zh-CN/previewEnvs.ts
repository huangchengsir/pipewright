export default {
  // 「PR 预览环境」大盘(R4 / E4.1 · 自托管版 Vercel / Netlify 预览部署)
  board: {
    eyebrow: '预览部署',
    title: 'PR 预览环境',
    subtitle:
      '每个 PR 一个临时环境 —— 独立子域名、自动证书、一次部署的容器,活在 pr-N-<项目>.<根域> 下。一眼看清某项目当前所有临时环境,合并或回收即拆除。',
    refreshing: '刷新中…',
    // 项目选择器
    projectLabel: '项目',
    projectAria: '选择项目',
    projectPick: '选择一个项目…',
    // 摘要条
    activeCount: '{n} 个活跃',
    reclaimedCount: '{n} 个已回收',
    // 卡片
    statusActive: '活跃',
    statusReclaimed: '已回收',
    createdAt: '创建于 {time}',
    reclaimedAt: '回收于 {time}',
    reclaim: '回收',
    reclaiming: '回收中…',
    retired: '已下线',
    // 回收确认
    reclaimConfirmTitle: '回收此预览环境?',
    reclaimConfirmBody: '将拆除 {sub} —— 移除 DNS 记录、停止容器并释放资源。此操作不可撤销。',
    reclaimed: '已回收预览环境',
    reclaimFail: '回收失败',
    // 空 / 无项目 / 错误态
    noProjectTitle: '还没有任何项目',
    noProjectDesc: '先创建一个项目并开启 PR 预览,提交 PR 后这里会列出对应的临时环境。',
    emptyTitle: '暂无预览环境',
    emptyDesc: '项目 {project} 当前没有活跃或历史的 PR 预览环境 —— 提交一个开启了预览的 PR 即可拉起第一个。',
    errTitle: '加载预览环境失败',
    errLoad: '加载失败({status})。',
    errReq: '请求失败({status})。',
    errNetwork: '网络错误,请稍后重试。',
    footNote: 'PR 合并或关闭时,对应预览环境会自动回收;你也可以随时在此手动回收以释放资源。',
  },
  // 每项目「PR 预览环境」配置卡(嵌在流水线「触发设置」)
  config: {
    title: 'PR 预览环境',
    subtitle: '开启后,每个 PR 运行都会自动拉起一个临时环境:在所选 DNS 提供商根域下生成子域名、签证书并绑定到该次部署。',
    on: '已开启',
    off: '已关闭',
    loading: '加载中…',
    retry: '重试',
    // 总开关
    enableLabel: '为每个 PR 自动创建预览环境',
    enableDesc: 'PR 运行时拉起临时环境,PR 关闭或手动回收时拆除。',
    // 提供商 + 根域
    providerLabel: 'DNS 提供商',
    providerNone: '选择一个 DNS 提供商…',
    noProviders: '尚未配置 DNS 提供商 —— 先去添加一个才能开启预览。',
    baseDomainLabel: '根域',
    baseDomainPlaceholder: '例:preview.example.com',
    // 链接形态预览
    exampleLabel: '链接形态:',
    invalidHint: '开启预览需选定一个 DNS 提供商,并填写合法的根域(FQDN)。',
    // 保存
    footNote: '保存后立即生效,影响此后该项目的所有 PR 运行。',
    save: '保存',
    saving: '保存中…',
    saved: '已保存预览环境配置',
    saveFail: '保存失败',
    errLoad: '加载失败({status})。',
    errReq: '请求失败({status})。',
    errNetwork: '网络错误,请稍后重试。',
  },
}
