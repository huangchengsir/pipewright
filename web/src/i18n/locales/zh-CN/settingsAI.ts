export default {
  title: 'AI 提供商',
  subtitle:
    'Pipewright 不自训模型 —— 接入你自己的 LLM 用于失败诊断与配置生成。密钥仅存于本实例的加密保险库,绝不外泄。',
  statusConfigured: '已配置',
  statusUnconfigured: '未配置',
  retry: '重试',

  providerClaudeTag: '诊断推荐',
  providerOllamaDesc: '本地 / 自托管',
  providerOllamaTag: '零外发',

  guidanceAria: 'AI 配置引导',
  guidanceTitle: '配置 LLM 以解锁 AI 诊断',
  guidanceBody:
    '接入 Claude、OpenAI 或本地 Ollama 后,流水线失败时 Pipewright 将自动生成根因假说与修复建议,无需手动排查日志。',

  selectProvider: '选择提供商',
  providerRadioAria: 'AI 提供商选择',
  selectProviderAria: '选择 {name}',
  providerConfig: '{name} 配置',
  lastSaved: '上次保存 {time}',

  apiKeyHint: '写入后仅显示掩码;留空则保留已存密钥',
  apiKeyReplacing: '正在替换…',
  apiKeyConfigured: '已配置 ••••(留空不变)',
  apiKeyPaste: '粘贴 API Key…',
  apiKeyMaskedAria: '已配置掩码: {masked}',

  ollamaHint: '本地 Ollama 无需 API Key —— 确保 Ollama 服务已在指定地址运行即可。',

  baseUrlLabel: '接入地址 (Base URL)',
  baseUrlHint: '默认: {url}',

  modelLabel: '模型',
  modelHint: '用于诊断的主模型,如 claude-opus-4-7 / gpt-4o / llama3',

  testConnection: '测试连接',
  testOk: '连接正常 · 延迟 {ms}ms',
  testFail: '连接失败',

  budgetLabel: '月 Token 上限',
  budgetHint: '超出后暂停 AI 诊断(留空=不限制;本期仅声明,下一 Epic 强制执行)',
  budgetPlaceholder: '如 500000,留空不限制',

  enableAi: '启用 AI 功能',
  enableAiDesc: '关闭时 AI 诊断静默跳过,核心 CI/CD 流水线不受影响',

  dirtyNote: '有未保存的改动',
  cleanNote: '暂无改动',
  discard: '放弃',
  saveChanges: '保存更改',

  toastSaveSuccess: 'AI 配置已保存',
  toastSaveFailed: '保存失败',

  errServerUnreachable: '无法连接到服务器,请检查后端是否运行后重试',
  errServerUnreachableShort: '无法连接到服务器',
  errVaultUnconfigured: '保险库未配置 master key,请设置 PIPEWRIGHT_MASTER_KEY 环境变量',
  errLoadFailed: '加载失败({status})',
  errLoadGeneric: '加载 AI 配置失败,请稍后重试',
  errBudgetInvalid: '月 token 上限须为正整数或留空',
  errProviderInvalid: '请选择有效的提供商',
  errBaseUrlRequired: '请填写接入地址',
  errApiKeyRequired: 'API Key 不可为空(非 Ollama 必填)',
  errRequestFailed: '请求失败({status})',
  errUnknown: '未知错误',
}
