export default {
  title: 'OAuth 應用',
  sectionDesc:
    '為每個 git 平台登記 OAuth 應用(Client ID / Secret),使用者即可在憑證保險庫一鍵「連接」帳號自動換取權杖,無需手動貼上 PAT。Secret 入庫即遮罩,寫入後不可讀出。',
  retry: '重試',

  providerCustomLabel: '自建',
  providerGiteeDesc: '碼雲',
  providerCustomDesc: '自架 GitLab / Gitea 等',

  statusEnabled: '已啟用',
  statusConfiguredIdle: '已設定·未啟用',
  statusUnconfigured: '未設定',

  clientIdPlaceholder: '在 git 平台 OAuth 應用頁取得',
  secretOptional: '(寫入後僅顯示遮罩)',
  secretPlaceholderKeep: '留空保留已存 Secret',
  secretPlaceholderNew: '貼上 Client Secret…',
  secretStored: '已存:{masked}',

  toggleAria: '啟用 {provider} OAuth',
  toggleTitle: '啟用連接',
  toggleDesc: '啟用後此平台的「連接」按鈕出現在憑證保險庫',

  lastSaved: '上次儲存 {time}',
  saveBtn: '儲存',

  errNoServer: '無法連接到伺服器,請檢查後端是否執行後重試',
  errVaultUnconfigured: '保險庫未設定 master key,請設定 PIPEWRIGHT_MASTER_KEY 環境變數',
  errLoadStatus: '載入失敗({status})',
  errLoadGeneric: '載入 OAuth 應用失敗,請稍後重試',

  errClientIdRequiredEnabled: '啟用時 Client ID 必填',
  errSecretRequiredEnabled: '啟用時需提供 Client Secret',
  errBaseUrlRequiredCustom: '自建實例需填寫 Base URL',
  errClientIdRequired: '請填寫 Client ID',
  errBaseUrlRequired: '請填寫 Base URL',

  toastSaveFailed: '儲存失敗',
  toastSaved: 'OAuth 應用已儲存',
  errNoServerShort: '無法連接到伺服器',
  unknownError: '未知錯誤',

  justNow: '剛剛',
  minutesAgo: '{n} 分鐘前',
  hoursAgo: '{n} 小時前',
  daysAgo: '{n} 天前',
}
