export default {
  title: 'OAuth 应用',
  sectionDesc:
    '为每个 git 平台登记 OAuth 应用(Client ID / Secret),用户即可在凭据保险库一键「连接」账号自动换取令牌,无需手动粘贴 PAT。Secret 落库即掩码,写入后不可读出。',
  retry: '重试',

  providerCustomLabel: '自建',
  providerGiteeDesc: '码云',
  providerCustomDesc: '自托管 GitLab / Gitea 等',

  statusEnabled: '已启用',
  statusConfiguredIdle: '已配置·未启用',
  statusUnconfigured: '未配置',

  clientIdPlaceholder: '在 git 平台 OAuth 应用页获取',
  secretOptional: '(写入后仅显示掩码)',
  secretPlaceholderKeep: '留空保留已存 Secret',
  secretPlaceholderNew: '粘贴 Client Secret…',
  secretStored: '已存:{masked}',

  toggleAria: '启用 {provider} OAuth',
  toggleTitle: '启用连接',
  toggleDesc: '启用后此平台的「连接」按钮出现在凭据保险库',

  lastSaved: '上次保存 {time}',
  saveBtn: '保存',

  errNoServer: '无法连接到服务器,请检查后端是否运行后重试',
  errVaultUnconfigured: '保险库未配置 master key,请设置 PIPEWRIGHT_MASTER_KEY 环境变量',
  errLoadStatus: '加载失败({status})',
  errLoadGeneric: '加载 OAuth 应用失败,请稍后重试',

  errClientIdRequiredEnabled: '启用时 Client ID 必填',
  errSecretRequiredEnabled: '启用时需提供 Client Secret',
  errBaseUrlRequiredCustom: '自建实例需填写 Base URL',
  errClientIdRequired: '请填写 Client ID',
  errBaseUrlRequired: '请填写 Base URL',

  toastSaveFailed: '保存失败',
  toastSaved: 'OAuth 应用已保存',
  errNoServerShort: '无法连接到服务器',
  unknownError: '未知错误',

  justNow: '刚刚',
  minutesAgo: '{n} 分钟前',
  hoursAgo: '{n} 小时前',
  daysAgo: '{n} 天前',
}
