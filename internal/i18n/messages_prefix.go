package i18n

// Concatenation-prefix messages (writeError(..., "<prefix>" + detail.Error())).
// T longest-prefix-matches these and preserves the appended detail verbatim.
// (镜像引用非法: is seeded in messages.go.)
func init() {
	registerPrefix(map[string]map[string]string{
		"镜像引用非法:": {
			"zh-TW": "映像引用非法:", "en": "Invalid image reference: ", "ja": "イメージ参照が不正です: ",
			"ko": "이미지 참조가 잘못되었습니다: ", "es": "Referencia de imagen no válida: ",
			"fr": "Référence d’image non valide : ", "de": "Ungültige Image-Referenz: ",
		},
		"shell 非法:": {
			"zh-TW": "shell 非法:", "en": "Invalid shell: ", "ja": "shell が不正です: ",
			"ko": "shell이 잘못되었습니다: ", "es": "Shell no válido: ", "fr": "Shell non valide : ", "de": "Ungültige Shell: ",
		},
		"创建参数非法:": {
			"zh-TW": "建立參數非法:", "en": "Invalid create parameters: ", "ja": "作成パラメータが不正です: ",
			"ko": "생성 매개변수가 잘못되었습니다: ", "es": "Parámetros de creación no válidos: ",
			"fr": "Paramètres de création non valides : ", "de": "Ungültige Erstellungsparameter: ",
		},
		"容器或 shell 非法:": {
			"zh-TW": "容器或 shell 非法:", "en": "Invalid container or shell: ", "ja": "コンテナまたは shell が不正です: ",
			"ko": "컨테이너 또는 shell이 잘못되었습니다: ", "es": "Contenedor o shell no válido: ",
			"fr": "Conteneur ou shell non valide : ", "de": "Ungültiger Container oder Shell: ",
		},
		"日志源或目标非法:": {
			"zh-TW": "日誌源或目標非法:", "en": "Invalid log source or target: ", "ja": "ログのソースまたはターゲットが不正です: ",
			"ko": "로그 소스 또는 대상이 잘못되었습니다: ", "es": "Origen o destino de registro no válido: ",
			"fr": "Source ou cible de journal non valide : ", "de": "Ungültige Log-Quelle oder -Ziel: ",
		},
		"服务类型/目标/操作非法:": {
			"zh-TW": "服務類型/目標/操作非法:", "en": "Invalid service type/target/action: ", "ja": "サービスの種類/ターゲット/操作が不正です: ",
			"ko": "서비스 유형/대상/작업이 잘못되었습니다: ", "es": "Tipo/destino/acción de servicio no válido: ",
			"fr": "Type/cible/action de service non valide : ", "de": "Ungültiger Diensttyp/-ziel/-aktion: ",
		},
		"清理范围非法:": {
			"zh-TW": "清理範圍非法:", "en": "Invalid prune scope: ", "ja": "クリーンアップ範囲が不正です: ",
			"ko": "정리 범위가 잘못되었습니다: ", "es": "Ámbito de limpieza no válido: ",
			"fr": "Portée de nettoyage non valide : ", "de": "Ungültiger Bereinigungsbereich: ",
		},
	})
}
