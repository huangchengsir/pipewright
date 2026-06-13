export default {
  title: 'DORA 指標',
  subtitle: '基於既有執行資料彙整的交付效能檢視 · 部署頻率 / 前置時間 / 變更失敗率 / 故障修復',
  generatedAt: '· 資料截至 {time}',

  window7d: '近 7 天',
  window30d: '近 30 天',
  window90d: '近 90 天',

  projectLabel: '專案',
  projectFilterAria: '依專案篩選',
  allProjects: '全部專案',
  windowAria: '時間視窗',

  errTitle: '載入 DORA 指標失敗',
  errOffline: '無法連線到伺服器,請確認後端是否執行後重試',
  errLoadStatus: '載入 DORA 指標失敗({status})',
  errLoadRetry: '載入 DORA 指標失敗,請稍後重試',

  summaryDeployments: '{days} 天內部署',
  summarySuccess: '成功',
  summaryFailed: '失敗',

  metricDeployFreq: '部署頻率',
  metricLeadTime: '變更前置時間',
  metricCfr: '變更失敗率',
  metricMttr: '平均修復時間',

  capDeployFreq: '{days} 天內 {count} 次成功部署',
  capLeadTime: '{count} 次成功部署的中位提交→上線時間',
  capLeadTimeEmpty: '尚無成功部署可統計前置時間',
  capCfr: '{failed} / {total} 次部署失敗',
  capMttr: '{count} 段「失敗→修復」的中位時間',
  capMttrEmpty: '視窗內無「失敗後修復」配對',

  noteLead: '口徑說明:一次「部署」= 一條進入終態的執行;前置時間在缺少提交時間時以入佇列時刻近似。DORA 指標基於 CI 執行資料為',
  noteEmphasis: '近似',
  noteTrail: '參考,不作為 SLA 依據。',
}
