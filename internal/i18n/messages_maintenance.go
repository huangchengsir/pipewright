package i18n

// Exact maintenance API messages retain zh-CN source strings at call sites.
func init() {
	register(map[string]map[string]string{
		"升级任务暂不可用，请核对原任务状态": {
			"zh-TW": "升級任務暫時無法使用，請確認原任務狀態",
			"en":    "Upgrade task is unavailable. Check the original task status.",
			"ja":    "アップグレードタスクを利用できません。元のタスクの状態を確認してください。",
			"ko":    "업그레이드 작업을 사용할 수 없습니다. 기존 작업 상태를 확인하세요.",
			"es":    "La tarea de actualización no está disponible. Comprueba el estado de la tarea original.",
			"fr":    "La tâche de mise à jour est indisponible. Vérifiez l’état de la tâche d’origine.",
			"de":    "Die Upgrade-Aufgabe ist nicht verfügbar. Prüfen Sie den Status der ursprünglichen Aufgabe.",
		},
		"请求格式错误": {
			"zh-TW": "請求格式錯誤",
			"en":    "Invalid request format",
			"ja":    "リクエストの形式が正しくありません",
			"ko":    "요청 형식이 올바르지 않습니다",
			"es":    "Formato de solicitud no válido",
			"fr":    "Format de requête invalide",
			"de":    "Ungültiges Anfrageformat",
		},
		"系统正在安装升级，请稍后重试": {
			"zh-TW": "系統正在安裝升級，請稍後再試",
			"en":    "The system is installing an upgrade. Please try again later.",
			"ja":    "システムのアップグレードをインストールしています。しばらくしてから再試行してください。",
			"ko":    "시스템 업그레이드를 설치 중입니다. 잠시 후 다시 시도하세요.",
			"es":    "El sistema está instalando una actualización. Inténtalo de nuevo más tarde.",
			"fr":    "Le système installe une mise à jour. Réessayez plus tard.",
			"de":    "Das System installiert ein Upgrade. Versuchen Sie es später erneut.",
		},
	})
}
