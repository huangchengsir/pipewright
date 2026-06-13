package i18n

func init() {
	register(map[string]map[string]string{
		"AI 命令助手未初始化": {
			"zh-TW": "AI 命令助手尚未初始化", "en": "AI command assistant service not initialized", "ja": "AI コマンドアシスタントが初期化されていません",
			"ko": "AI 명령 도우미가 초기화되지 않았습니다", "es": "El asistente de comandos de IA no está inicializado", "fr": "L'assistant de commandes IA n'est pas initialisé", "de": "KI-Befehlsassistent ist nicht initialisiert",
		},
		"OAuth 服务未初始化": {
			"zh-TW": "OAuth 服務尚未初始化", "en": "OAuth service not initialized", "ja": "OAuth サービスが初期化されていません",
			"ko": "OAuth 서비스가 초기화되지 않았습니다", "es": "El servicio OAuth no está inicializado", "fr": "Le service OAuth n'est pas initialisé", "de": "OAuth-Dienst ist nicht initialisiert",
		},
		"compose 内容过大": {
			"zh-TW": "compose 內容過大", "en": "compose content too large", "ja": "compose の内容が大きすぎます",
			"ko": "compose 내용이 너무 큽니다", "es": "El contenido de compose es demasiado grande", "fr": "Le contenu compose est trop volumineux", "de": "compose-Inhalt ist zu groß",
		},
		"page 必须为不小于 1 的整数": {
			"zh-TW": "page 必須為不小於 1 的整數", "en": "page must be an integer no less than 1", "ja": "page は 1 以上の整数である必要があります",
			"ko": "page는 1 이상의 정수여야 합니다", "es": "page debe ser un entero no menor que 1", "fr": "page doit être un entier supérieur ou égal à 1", "de": "page muss eine Ganzzahl von mindestens 1 sein",
		},
		"secret 不能为空": {
			"zh-TW": "secret 不能為空", "en": "secret must not be empty", "ja": "secret を空にすることはできません",
			"ko": "secret은 비워 둘 수 없습니다", "es": "secret no puede estar vacío", "fr": "secret ne peut pas être vide", "de": "secret darf nicht leer sein",
		},
		"webhook 端点不存在": {
			"zh-TW": "webhook 端點不存在", "en": "webhook endpoint not found", "ja": "webhook エンドポイントが見つかりません",
			"ko": "webhook 엔드포인트를 찾을 수 없습니다", "es": "Endpoint de webhook no encontrado", "fr": "Point de terminaison webhook introuvable", "de": "webhook-Endpunkt nicht gefunden",
		},
		"下游目标数量超过上限": {
			"zh-TW": "下游目標數量超過上限", "en": "number of downstream targets exceeds the limit", "ja": "下流ターゲットの数が上限を超えています",
			"ko": "다운스트림 대상 수가 한도를 초과했습니다", "es": "El número de destinos descendentes supera el límite", "fr": "Le nombre de cibles en aval dépasse la limite", "de": "Anzahl der nachgelagerten Ziele überschreitet das Limit",
		},
		"产物不存在": {
			"zh-TW": "產物不存在", "en": "artifact not found", "ja": "成果物が見つかりません",
			"ko": "아티팩트를 찾을 수 없습니다", "es": "Artefacto no encontrado", "fr": "Artefact introuvable", "de": "Artefakt nicht gefunden",
		},
		"代码管理区未启用,无法列提交": {
			"zh-TW": "程式碼管理區未啟用,無法列出提交", "en": "code management area is not enabled; cannot list commits", "ja": "コード管理エリアが有効になっていないため、コミットを一覧表示できません",
			"ko": "코드 관리 영역이 활성화되지 않아 커밋을 나열할 수 없습니다", "es": "El área de gestión de código no está habilitada; no se pueden listar los commits", "fr": "La zone de gestion du code n'est pas activée ; impossible de lister les commits", "de": "Code-Verwaltungsbereich ist nicht aktiviert; Commits können nicht aufgelistet werden",
		},
		"保险库未配置 master key": {
			"zh-TW": "保險庫未配置 master key", "en": "vault has no master key configured", "ja": "保管庫に master key が設定されていません",
			"ko": "보관소에 master key가 구성되지 않았습니다", "es": "La bóveda no tiene configurada una master key", "fr": "Le coffre-fort n'a pas de master key configurée", "de": "Im Tresor ist kein master key konfiguriert",
		},
		"保险库未配置 master key,无法校验 secret 变量引用": {
			"zh-TW": "保險庫未配置 master key,無法校驗 secret 變數引用", "en": "vault has no master key configured; cannot validate secret variable references", "ja": "保管庫に master key が設定されていないため、secret 変数の参照を検証できません",
			"ko": "보관소에 master key가 구성되지 않아 secret 변수 참조를 검증할 수 없습니다", "es": "La bóveda no tiene configurada una master key; no se pueden validar las referencias a variables secret", "fr": "Le coffre-fort n'a pas de master key configurée ; impossible de valider les références aux variables secret", "de": "Im Tresor ist kein master key konfiguriert; secret-Variablenreferenzen können nicht validiert werden",
		},
		"凭据无效或无权限,无法访问该仓库": {
			"zh-TW": "憑據無效或無權限,無法存取該倉庫", "en": "credential is invalid or unauthorized; cannot access the repository", "ja": "認証情報が無効または権限がないため、このリポジトリにアクセスできません",
			"ko": "자격 증명이 유효하지 않거나 권한이 없어 해당 저장소에 접근할 수 없습니다", "es": "La credencial es inválida o no autorizada; no se puede acceder al repositorio", "fr": "L'identifiant est invalide ou non autorisé ; impossible d'accéder au dépôt", "de": "Anmeldedaten sind ungültig oder nicht autorisiert; Zugriff auf das Repository nicht möglich",
		},
		"制品库中找不到该产物字节": {
			"zh-TW": "製品庫中找不到該產物位元組", "en": "artifact bytes not found in the artifact store", "ja": "成果物ストアに該当する成果物のバイトが見つかりません",
			"ko": "아티팩트 저장소에서 해당 아티팩트 바이트를 찾을 수 없습니다", "es": "No se encontraron los bytes del artefacto en el almacén de artefactos", "fr": "Octets de l'artefact introuvables dans le magasin d'artefacts", "de": "Artefakt-Bytes im Artefaktspeicher nicht gefunden",
		},
		"变量 key 不能为空": {
			"zh-TW": "變數 key 不能為空", "en": "variable key must not be empty", "ja": "変数 key を空にすることはできません",
			"ko": "변수 key는 비워 둘 수 없습니다", "es": "La key de la variable no puede estar vacía", "fr": "La key de la variable ne peut pas être vide", "de": "Variablen-key darf nicht leer sein",
		},
		"变量键不能为空且同作用域内不可重复,secret 项须指定保险库凭据": {
			"zh-TW": "變數鍵不能為空且同作用域內不可重複,secret 項須指定保險庫憑據", "en": "variable key must not be empty and must be unique within the same scope; secret entries must specify a vault credential", "ja": "変数キーは空にできず、同一スコープ内で重複できません。secret 項目には保管庫の認証情報を指定する必要があります",
			"ko": "변수 키는 비워 둘 수 없으며 동일 범위 내에서 중복될 수 없습니다. secret 항목은 보관소 자격 증명을 지정해야 합니다", "es": "La clave de la variable no puede estar vacía y debe ser única dentro del mismo ámbito; las entradas secret deben especificar una credencial de bóveda", "fr": "La clé de la variable ne peut pas être vide et doit être unique dans la même portée ; les entrées secret doivent spécifier un identifiant de coffre-fort", "de": "Variablenschlüssel darf nicht leer sein und muss innerhalb desselben Geltungsbereichs eindeutig sein; secret-Einträge müssen Tresor-Anmeldedaten angeben",
		},
		"定时触发服务未初始化": {
			"zh-TW": "定時觸發服務尚未初始化", "en": "scheduled trigger service not initialized", "ja": "スケジュールトリガーサービスが初期化されていません",
			"ko": "예약 트리거 서비스가 초기화되지 않았습니다", "es": "El servicio de activación programada no está inicializado", "fr": "Le service de déclenchement planifié n'est pas initialisé", "de": "Dienst für geplante Auslöser ist nicht initialisiert",
		},
		"差异对比所需服务未初始化": {
			"zh-TW": "差異比對所需服務尚未初始化", "en": "service required for diff comparison not initialized", "ja": "差分比較に必要なサービスが初期化されていません",
			"ko": "차이 비교에 필요한 서비스가 초기화되지 않았습니다", "es": "El servicio requerido para la comparación de diferencias no está inicializado", "fr": "Le service requis pour la comparaison des différences n'est pas initialisé", "de": "Der für den Diff-Vergleich erforderliche Dienst ist nicht initialisiert",
		},
		"引用的 SSH 凭据不存在": {
			"zh-TW": "引用的 SSH 憑據不存在", "en": "referenced SSH credential not found", "ja": "参照された SSH 認証情報が見つかりません",
			"ko": "참조된 SSH 자격 증명을 찾을 수 없습니다", "es": "La credencial SSH referenciada no se encuentra", "fr": "L'identifiant SSH référencé est introuvable", "de": "Referenzierte SSH-Anmeldedaten nicht gefunden",
		},
		"新口令至少 8 位": {
			"zh-TW": "新密碼至少 8 位", "en": "new password must be at least 8 characters", "ja": "新しいパスワードは 8 文字以上である必要があります",
			"ko": "새 비밀번호는 8자 이상이어야 합니다", "es": "La nueva contraseña debe tener al menos 8 caracteres", "fr": "Le nouveau mot de passe doit comporter au moins 8 caractères", "de": "Das neue Passwort muss mindestens 8 Zeichen lang sein",
		},
		"晋级服务未初始化": {
			"zh-TW": "晉級服務尚未初始化", "en": "promotion service not initialized", "ja": "昇格サービスが初期化されていません",
			"ko": "승격 서비스가 초기화되지 않았습니다", "es": "El servicio de promoción no está inicializado", "fr": "Le service de promotion n'est pas initialisé", "de": "Promotion-Dienst ist nicht initialisiert",
		},
		"服务器服务未初始化": {
			"zh-TW": "伺服器服務尚未初始化", "en": "server service not initialized", "ja": "サーバーサービスが初期化されていません",
			"ko": "서버 서비스가 초기화되지 않았습니다", "es": "El servicio de servidor no está inicializado", "fr": "Le service serveur n'est pas initialisé", "de": "Server-Dienst ist nicht initialisiert",
		},
		"构建配置非法": {
			"zh-TW": "建置配置非法", "en": "invalid build configuration", "ja": "ビルド設定が無効です",
			"ko": "빌드 구성이 유효하지 않습니다", "es": "Configuración de compilación inválida", "fr": "Configuration de build invalide", "de": "Ungültige Build-Konfiguration",
		},
		"模板引用的通知渠道不存在": {
			"zh-TW": "範本引用的通知通道不存在", "en": "notification channel referenced by the template not found", "ja": "テンプレートが参照する通知チャネルが見つかりません",
			"ko": "템플릿이 참조하는 알림 채널을 찾을 수 없습니다", "es": "El canal de notificación referenciado por la plantilla no se encuentra", "fr": "Le canal de notification référencé par le modèle est introuvable", "de": "Der vom Template referenzierte Benachrichtigungskanal wurde nicht gefunden",
		},
		"流水线配置服务未初始化": {
			"zh-TW": "流水線配置服務尚未初始化", "en": "pipeline configuration service not initialized", "ja": "パイプライン設定サービスが初期化されていません",
			"ko": "파이프라인 구성 서비스가 초기화되지 않았습니다", "es": "El servicio de configuración de pipeline no está inicializado", "fr": "Le service de configuration de pipeline n'est pas initialisé", "de": "Pipeline-Konfigurationsdienst ist nicht initialisiert",
		},
		"源运行未成功,不可晋级": {
			"zh-TW": "來源執行未成功,不可晉級", "en": "source run did not succeed; cannot promote", "ja": "ソースの実行が成功していないため、昇格できません",
			"ko": "소스 실행이 성공하지 않아 승격할 수 없습니다", "es": "La ejecución de origen no fue exitosa; no se puede promover", "fr": "L'exécution source n'a pas réussi ; promotion impossible", "de": "Quell-Ausführung war nicht erfolgreich; Promotion nicht möglich",
		},
		"环境服务未初始化": {
			"zh-TW": "環境服務尚未初始化", "en": "environment service not initialized", "ja": "環境サービスが初期化されていません",
			"ko": "환경 서비스가 초기화되지 않았습니다", "es": "El servicio de entorno no está inicializado", "fr": "Le service d'environnement n'est pas initialisé", "de": "Umgebungsdienst ist nicht initialisiert",
		},
		"目标服务器不存在": {
			"zh-TW": "目標伺服器不存在", "en": "target server not found", "ja": "ターゲットサーバーが見つかりません",
			"ko": "대상 서버를 찾을 수 없습니다", "es": "Servidor de destino no encontrado", "fr": "Serveur cible introuvable", "de": "Zielserver nicht gefunden",
		},
		"网络名非法": {
			"zh-TW": "網路名稱非法", "en": "invalid network name", "ja": "ネットワーク名が無効です",
			"ko": "네트워크 이름이 유효하지 않습니다", "es": "Nombre de red inválido", "fr": "Nom de réseau invalide", "de": "Ungültiger Netzwerkname",
		},
		"自建(custom)provider 必须配置 baseUrl": {
			"zh-TW": "自建(custom)provider 必須配置 baseUrl", "en": "custom provider must have baseUrl configured", "ja": "自前(custom)の provider には baseUrl を設定する必要があります",
			"ko": "사용자 지정(custom) provider는 baseUrl을 구성해야 합니다", "es": "El provider personalizado (custom) debe tener baseUrl configurado", "fr": "Le provider personnalisé (custom) doit avoir baseUrl configuré", "de": "Der benutzerdefinierte (custom) provider muss baseUrl konfiguriert haben",
		},
		"该 Stack 无单一可写 compose 路径,无法在线保存": {
			"zh-TW": "該 Stack 無單一可寫 compose 路徑,無法線上儲存", "en": "this Stack has no single writable compose path; cannot save online", "ja": "この Stack には書き込み可能な単一の compose パスがないため、オンラインで保存できません",
			"ko": "이 Stack에는 쓰기 가능한 단일 compose 경로가 없어 온라인으로 저장할 수 없습니다", "es": "Este Stack no tiene una única ruta de compose escribible; no se puede guardar en línea", "fr": "Cette Stack n'a pas de chemin compose unique modifiable ; impossible d'enregistrer en ligne", "de": "Dieser Stack hat keinen einzelnen beschreibbaren compose-Pfad; Online-Speichern nicht möglich",
		},
		"该环境暂无部署历史": {
			"zh-TW": "該環境暫無部署歷史", "en": "this environment has no deployment history yet", "ja": "この環境にはまだデプロイ履歴がありません",
			"ko": "이 환경에는 아직 배포 이력이 없습니다", "es": "Este entorno aún no tiene historial de despliegues", "fr": "Cet environnement n'a pas encore d'historique de déploiement", "de": "Diese Umgebung hat noch keinen Bereitstellungsverlauf",
		},
		"该运行已晋级到该环境": {
			"zh-TW": "該執行已晉級到該環境", "en": "this run has already been promoted to this environment", "ja": "この実行はすでにこの環境へ昇格されています",
			"ko": "이 실행은 이미 해당 환경으로 승격되었습니다", "es": "Esta ejecución ya ha sido promovida a este entorno", "fr": "Cette exécution a déjà été promue vers cet environnement", "de": "Diese Ausführung wurde bereits in diese Umgebung übernommen",
		},
		"请先登录": {
			"zh-TW": "請先登入", "en": "please log in first", "ja": "先にログインしてください",
			"ko": "먼저 로그인하세요", "es": "Inicie sesión primero", "fr": "Veuillez d'abord vous connecter", "de": "Bitte zuerst anmelden",
		},
		"请求体过大或读取失败": {
			"zh-TW": "請求體過大或讀取失敗", "en": "request body too large or failed to read", "ja": "リクエストボディが大きすぎるか、読み取りに失敗しました",
			"ko": "요청 본문이 너무 크거나 읽기에 실패했습니다", "es": "El cuerpo de la solicitud es demasiado grande o no se pudo leer", "fr": "Le corps de la requête est trop volumineux ou la lecture a échoué", "de": "Anfrage-Body ist zu groß oder konnte nicht gelesen werden",
		},
		"路径过滤 glob 不能含空白(如 backend/**、*.go)": {
			"zh-TW": "路徑過濾 glob 不能含空白(如 backend/**、*.go)", "en": "path filter glob must not contain whitespace (e.g. backend/**, *.go)", "ja": "パスフィルターの glob に空白を含めることはできません(例: backend/**、*.go)",
			"ko": "경로 필터 glob에는 공백이 포함될 수 없습니다(예: backend/**, *.go)", "es": "El glob del filtro de rutas no puede contener espacios en blanco (p. ej. backend/**, *.go)", "fr": "Le glob de filtre de chemin ne doit pas contenir d'espaces (p. ex. backend/**, *.go)", "de": "Der Pfadfilter-glob darf keine Leerzeichen enthalten (z. B. backend/**, *.go)",
		},
		"运行服务未初始化": {
			"zh-TW": "執行服務尚未初始化", "en": "run service not initialized", "ja": "実行サービスが初期化されていません",
			"ko": "실행 서비스가 초기화되지 않았습니다", "es": "El servicio de ejecución no está inicializado", "fr": "Le service d'exécution n'est pas initialisé", "de": "Ausführungsdienst ist nicht initialisiert",
		},
		"通知路由不存在": {
			"zh-TW": "通知路由不存在", "en": "notification route not found", "ja": "通知ルートが見つかりません",
			"ko": "알림 라우트를 찾을 수 없습니다", "es": "Ruta de notificación no encontrada", "fr": "Route de notification introuvable", "de": "Benachrichtigungsroute nicht gefunden",
		},
		"项目不存在": {
			"zh-TW": "專案不存在", "en": "project not found", "ja": "プロジェクトが見つかりません",
			"ko": "프로젝트를 찾을 수 없습니다", "es": "Proyecto no encontrado", "fr": "Projet introuvable", "de": "Projekt nicht gefunden",
		},
		"风险标注所需服务未初始化": {
			"zh-TW": "風險標註所需服務尚未初始化", "en": "service required for risk annotation not initialized", "ja": "リスク注釈に必要なサービスが初期化されていません",
			"ko": "위험 주석에 필요한 서비스가 초기화되지 않았습니다", "es": "El servicio requerido para la anotación de riesgos no está inicializado", "fr": "Le service requis pour l'annotation des risques n'est pas initialisé", "de": "Der für die Risikoannotation erforderliche Dienst ist nicht initialisiert",
		},
	})
}
