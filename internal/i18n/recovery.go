package i18n

func init() {
	register(map[string]map[string]string{
		"节点恢复尝试": {"zh-TW": "節點恢復嘗試", "en": "Node recovery attempt", "ja": "ノード復旧の試行", "ko": "노드 복구 시도", "es": "Intento de recuperación de nodo", "fr": "Tentative de récupération de nœud", "de": "Wiederherstellungsversuch des Knotens"},
		"原始运行":   {"zh-TW": "原始執行", "en": "Original run", "ja": "元の実行", "ko": "원본 실행", "es": "Ejecución original", "fr": "Exécution initiale", "de": "Ursprünglicher Lauf"},
		"来源执行":   {"zh-TW": "來源執行", "en": "Source attempt", "ja": "元の試行", "ko": "원본 시도", "es": "Intento de origen", "fr": "Tentative source", "de": "Quellversuch"},
		"恢复发起人":  {"zh-TW": "恢復發起人", "en": "Recovery initiated by", "ja": "復旧の開始者", "ko": "복구 요청자", "es": "Recuperación iniciada por", "fr": "Récupération lancée par", "de": "Wiederherstellung angefordert von"},
	})
}
