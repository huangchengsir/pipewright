export default {
  "repairCredentialDescription": "為這個專案重新選擇儲存庫憑據。只有按下儲存才會更新專案。",
  "title": "首次成功執行",
  "intro": "建立專案，準備流水線，再由你確認執行。",
  "projectLabel": "目前專案",
  "resultProjectLabel": "成功執行所屬專案",
  "projectUnavailable": "專案名稱暫時無法取得",
  "stepsLabel": "上手步驟",
  "steps": {
    "project": "建立專案",
    "pipeline": "準備流水線",
    "run": "成功執行一次"
  },
  "stepDescriptions": {
    "project": "連接自己的儲存庫。目前建立專案需要儲存庫憑據。",
    "pipeline": "儲存適用設定，檢查實際任務需要的憑據和執行環境；不要求無關伺服器。",
    "run": "在既有確認畫面主動提交任務，查看流水線執行結果。"
  },
  "stepStates": {
    "saved": "已儲存",
    "done": "已完成",
    "current": "目前步驟",
    "pending": "待進行",
    "unknown": "待確認"
  },
  "states": {
    "loading": "正在讀取實例狀態",
    "unknown": "暫時無法確認",
    "create": "先建立專案",
    "queued": "執行排隊中",
    "running": "流水線執行中",
    "waiting_approval": "等待核准",
    "failed": "查看最近失敗",
    "configure": "繼續準備流水線",
    "unconfirmed": "核對流水線設定",
    "repository": "核對儲存庫流水線",
    "ready": "可以前往執行",
    "success": "流水線執行成功",
    "legacy": "已有成功執行紀錄",
    "stub": "示範執行尚不計入完成",
    "mixed": "混合執行尚不計入完成",
    "pending": "執行來源待確認"
  },
  "descriptions": {
    "loading": "只讀取目前實例的資料。仍可跳過或返回工作台。",
    "unknown": "查詢未能確認完整狀態，不代表沒有專案或成功紀錄。請重試。",
    "create": "使用既有專案建立流程，選擇儲存庫憑據並填寫預設分支。",
    "queued": "查看所選專案的最新任務；無需再次提交。",
    "running": "查看最新執行的進度與結果。",
    "waiting_approval": "前往執行詳情查看核准狀態；引導不會自動核准。",
    "failed": "前往該次執行查看日誌，修復同一專案後再執行。",
    "configure": "檢查下列適用缺項。登記伺服器不代表連線已驗證。",
    "unconfirmed": "目前無法確認設定已就緒。可核對設定，也可由原有入口手動執行。",
    "repository": "此專案使用儲存庫 .pipewright.yml。引導不會擷取儲存庫，也不要求另存 UI 設定。",
    "ready": "開啟此專案的執行確認框。任務只在你提交後執行。",
    "success": "實例已有真實執行的成功流水線紀錄。這不額外證明部署或服務連線。",
    "legacy": "歷史成功紀錄符合相容規則；無法據此額外證明真實建置或部署。",
    "stub": "這次成功包含示範執行，不能證明真實任務已完成。",
    "mixed": "這次執行同時包含真實與示範任務，不能計為首次真實成功。",
    "pending": "執行已成功，但執行證據仍未確認；不會推斷為真實成功。"
  },
  "actions": {
    "create": "建立專案",
    "configure": "繼續設定",
    "checkPipeline": "核對流水線",
    "goRun": "前往執行",
    "viewRun": "查看執行",
    "viewFailed": "查看失敗日誌",
    "edit": "修改流水線",
    "viewResult": "查看執行結果",
    "autoTrigger": "設定自動觸發",
    "runtimeHelp": "執行環境說明",
    "retry": "重試",
    "open": "開啟此步驟",
    "servers": "登記伺服器",
    "repairCredential": "修改儲存庫憑據",
    "credentials": "管理憑據"
  },
  "issues": {
    "build": "檢查目前建置方式需要的設定和工具鏈。",
    "tasks": "檢查任務類型、映像與命令；目前執行器可能不支援部分任務。",
    "credentials": "實際使用的憑據參照缺失。",
    "projectCredential": "專案儲存庫憑據缺失，請在專案中重新選擇。",
    "vault": "憑據保險庫未設定主密鑰，現有憑據無法確認可用。",
    "environment": "目前分支參照的環境尚未定義。",
    "noTasks": "目前分支沒有可執行的實際任務。",
    "notification": "實際通知任務參照的通道不存在。",
    "pipeline": "流水線結構需要核對。",
    "runtime": "目前啟動的執行器為示範模式。",
    "server": "節點或遠端執行器參照的伺服器不存在。",
    "storage": "相關設定暫時無法讀取，請重試。"
  },
  "runtime": {
    "stub": "目前實際使用示範執行器，無法執行真實任務。",
    "available": "已選用非示範執行器，本機容器 CLI 已被辨識；這不是容器服務連線證明。",
    "unknown": "執行能力或遠端連線尚未確認；不會自動探測連線。",
    "instructions": "檢查執行 Pipewright 的主機或容器的容器 CLI、服務權限與連線設定。遠端執行器在對應專案的變數設定中設定。這裡只提供說明，不安裝軟體、不測試 SSH，也不提交任務。"
  }
}
