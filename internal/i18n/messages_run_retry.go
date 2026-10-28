package i18n

func init() {
	register(map[string]map[string]string{
		"恢复操作暂不可用，请核对原操作与恢复证据": {
			"zh-TW": "恢復操作暫不可用，請核對原操作與恢復證據",
			"en":    "Recovery is unavailable. Check the original operation and recovery evidence.",
			"ja":    "復旧操作を利用できません。元の操作と復旧の証拠を確認してください。",
			"ko":    "복구를 사용할 수 없습니다. 원래 작업과 복구 근거를 확인하세요.",
			"es":    "La recuperación no está disponible. Comprueba la operación original y las pruebas de recuperación.",
			"fr":    "La reprise est indisponible. Vérifiez l’opération initiale et les preuves de reprise.",
			"de":    "Wiederaufnahme nicht verfügbar. Prüfen Sie den ursprünglichen Vorgang und die Nachweise.",
		},
		"分页参数非法": {"zh-TW": "分頁參數無效", "en": "Invalid pagination parameters", "ja": "ページ指定が無効です", "ko": "페이지 매개변수가 올바르지 않습니다", "es": "Parámetros de paginación no válidos", "fr": "Paramètres de pagination invalides", "de": "Ungültige Seitenparameter"},
	})
}
