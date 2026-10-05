export default {
  "title": "流水線操作引導",
  "locate": "定位操作",
  "review": "設定已填寫，繼續",
  "refresh": "重新檢查",
  "close": "退出操作引導",
  "unsaved": "草稿尚未儲存；退出引導會保留編輯內容。",
  "phases": {
    "loading": {
      "title": "正在檢查設定",
      "description": "只讀取目前實例的狀態。"
    },
    "unknown": {
      "title": "狀態待確認",
      "description": "無法確認設定，請重新檢查；未知不算就緒。"
    },
    "repository": {
      "title": "核對儲存庫設定",
      "description": "專案使用 .pipewright.yml；不要求另存 UI 草稿，也不自動拉取儲存庫。"
    },
    "stage": {
      "title": "新增任務階段",
      "description": "點擊畫布中的新增階段。來源節點外還需要實際任務。"
    },
    "task": {
      "title": "新增第一個任務",
      "description": "點擊階段下方的串行節點，開啟任務類型選擇。"
    },
    "choose": {
      "title": "選擇任務類型",
      "description": "選擇適合專案的任務。首次可用自訂指令碼；映像與命令由你填寫。"
    },
    "configure": {
      "title": "填寫任務設定",
      "description": "定位並開啟任務，在右側填寫必要設定。指令碼需要映像和命令；填好後點擊繼續。"
    },
    "save": {
      "title": "儲存並檢查",
      "description": "點擊頁首儲存草稿；成功後重新檢查缺項，填寫不等於就緒。"
    },
    "issues": {
      "title": "已儲存，仍有缺項",
      "description": "點擊缺項前往對應位置。已有任務但分支沒有任務時，檢查階段條件和分支匹配再儲存。"
    },
    "runtime": {
      "title": "準備執行環境",
      "description": "已儲存，但執行能力尚未滿足。再次儲存不會解決示範執行器問題。"
    },
    "ready": {
      "title": "設定就緒，可確認執行",
      "description": "必要設定、引用和執行器檢查通過。前往執行只開啟確認框；不代表連線已驗證。"
    }
  }
}
