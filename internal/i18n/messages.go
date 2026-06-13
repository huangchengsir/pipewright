package i18n

// catalog maps each zh-CN source message to its translations.
// Keys are the EXACT zh-CN string passed to writeError at the call site.
// zh-CN itself is omitted (T returns the key for the default locale).
//
// NOTE: this is the seed set; the full set is populated from every writeError
// call site. Missing entries pass through as zh-CN (always safe).
var catalog = map[string]map[string]string{
	"服务器内部错误": {
		"zh-TW": "伺服器內部錯誤", "en": "Internal server error", "ja": "サーバー内部エラー",
		"ko": "서버 내부 오류", "es": "Error interno del servidor", "fr": "Erreur interne du serveur", "de": "Interner Serverfehler",
	},
	"项目不存在": {
		"zh-TW": "專案不存在", "en": "Project not found", "ja": "プロジェクトが見つかりません",
		"ko": "프로젝트를 찾을 수 없습니다", "es": "Proyecto no encontrado", "fr": "Projet introuvable", "de": "Projekt nicht gefunden",
	},
	"运行不存在": {
		"zh-TW": "執行不存在", "en": "Run not found", "ja": "実行が見つかりません",
		"ko": "실행을 찾을 수 없습니다", "es": "Ejecución no encontrada", "fr": "Exécution introuvable", "de": "Ausführung nicht gefunden",
	},
	"服务器不存在": {
		"zh-TW": "伺服器不存在", "en": "Server not found", "ja": "サーバーが見つかりません",
		"ko": "서버를 찾을 수 없습니다", "es": "Servidor no encontrado", "fr": "Serveur introuvable", "de": "Server nicht gefunden",
	},
	"凭据不存在": {
		"zh-TW": "憑證不存在", "en": "Credential not found", "ja": "認証情報が見つかりません",
		"ko": "자격 증명을 찾을 수 없습니다", "es": "Credencial no encontrada", "fr": "Identifiant introuvable", "de": "Anmeldedaten nicht gefunden",
	},
	"保险库未配置 master key": {
		"zh-TW": "保險庫未配置 master key", "en": "Vault has no master key configured", "ja": "Vault に master key が設定されていません",
		"ko": "볼트에 master key가 구성되지 않았습니다", "es": "La bóveda no tiene master key configurada",
		"fr": "Le coffre n’a pas de master key configurée", "de": "Im Tresor ist kein Master Key konfiguriert",
	},
	"请求体格式错误": {
		"zh-TW": "請求體格式錯誤", "en": "Malformed request body", "ja": "リクエストボディの形式が不正です",
		"ko": "요청 본문 형식이 잘못되었습니다", "es": "Cuerpo de la solicitud con formato incorrecto",
		"fr": "Corps de requête mal formé", "de": "Fehlerhafter Anfragetext",
	},
	"CSRF token 缺失或不匹配": {
		"zh-TW": "CSRF token 缺失或不符", "en": "CSRF token missing or mismatched", "ja": "CSRF トークンが欠落または不一致です",
		"ko": "CSRF 토큰이 없거나 일치하지 않습니다", "es": "Token CSRF ausente o no coincidente",
		"fr": "Jeton CSRF manquant ou non concordant", "de": "CSRF-Token fehlt oder stimmt nicht überein",
	},
}

// prefixCatalog handles messages built by concatenation ("<prefix>" + detail).
// T does a longest-prefix match and preserves the appended detail verbatim.
var prefixCatalog = map[string]map[string]string{
	"镜像引用非法:": {
		"zh-TW": "映像引用非法:", "en": "Invalid image reference: ", "ja": "イメージ参照が不正です: ",
		"ko": "이미지 참조가 잘못되었습니다: ", "es": "Referencia de imagen no válida: ",
		"fr": "Référence d’image non valide : ", "de": "Ungültige Image-Referenz: ",
	},
	"创建参数非法:": {
		"zh-TW": "建立參數非法:", "en": "Invalid create parameters: ", "ja": "作成パラメータが不正です: ",
		"ko": "생성 매개변수가 잘못되었습니다: ", "es": "Parámetros de creación no válidos: ",
		"fr": "Paramètres de création non valides : ", "de": "Ungültige Erstellungsparameter: ",
	},
}
