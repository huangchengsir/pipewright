package i18n

func init() {
	register(map[string]map[string]string{
		"AI 配置服务未初始化": {
			"zh-TW": "AI 設定服務未初始化", "en": "AI configuration service not initialized", "ja": "AI 設定サービスが初期化されていません",
			"ko": "AI 구성 서비스가 초기화되지 않았습니다", "es": "El servicio de configuración de IA no está inicializado", "fr": "Le service de configuration de l'IA n'est pas initialisé", "de": "KI-Konfigurationsdienst nicht initialisiert",
		},
		"clientId 不能为空": {
			"zh-TW": "clientId 不能為空", "en": "clientId must not be empty", "ja": "clientId は空にできません",
			"ko": "clientId는 비워 둘 수 없습니다", "es": "clientId no debe estar vacío", "fr": "clientId ne doit pas être vide", "de": "clientId darf nicht leer sein",
		},
		"name 不能为空": {
			"zh-TW": "name 不能為空", "en": "name must not be empty", "ja": "name は空にできません",
			"ko": "name은 비워 둘 수 없습니다", "es": "name no debe estar vacío", "fr": "name ne doit pas être vide", "de": "name darf nicht leer sein",
		},
		"provider 只能为 gitee、github、gitlab 或 custom": {
			"zh-TW": "provider 只能為 gitee、github、gitlab 或 custom", "en": "provider must be one of gitee, github, gitlab or custom", "ja": "provider は gitee、github、gitlab、custom のいずれかである必要があります",
			"ko": "provider는 gitee, github, gitlab 또는 custom 중 하나여야 합니다", "es": "provider solo puede ser gitee, github, gitlab o custom", "fr": "provider doit être gitee, github, gitlab ou custom", "de": "provider muss gitee, github, gitlab oder custom sein",
		},
		"webhook 地址不被允许:不可指向云元数据或链路本地地址": {
			"zh-TW": "webhook 位址不被允許:不可指向雲端中繼資料或鏈路本地位址", "en": "webhook URL not allowed: must not point to cloud metadata or link-local addresses", "ja": "webhook URL は許可されていません:クラウドメタデータまたはリンクローカルアドレスを指すことはできません",
			"ko": "webhook 주소가 허용되지 않습니다: 클라우드 메타데이터 또는 링크 로컬 주소를 가리킬 수 없습니다", "es": "URL de webhook no permitida: no debe apuntar a metadatos de la nube ni a direcciones link-local", "fr": "URL de webhook non autorisée : ne doit pas pointer vers les métadonnées du cloud ni vers des adresses link-local", "de": "webhook-URL nicht zulässig: darf nicht auf Cloud-Metadaten oder Link-Local-Adressen verweisen",
		},
		"上一次成功部署无可重发产物": {
			"zh-TW": "上一次成功部署無可重發產物", "en": "no redeployable artifact from the last successful deployment", "ja": "前回の成功したデプロイに再デプロイ可能な成果物がありません",
			"ko": "마지막 성공한 배포에 재배포할 산출물이 없습니다", "es": "no hay artefactos redesplegables del último despliegue exitoso", "fr": "aucun artefact redéployable issu du dernier déploiement réussi", "de": "kein erneut bereitstellbares Artefakt aus der letzten erfolgreichen Bereitstellung",
		},
		"主机地址不能为空": {
			"zh-TW": "主機位址不能為空", "en": "host address must not be empty", "ja": "ホストアドレスは空にできません",
			"ko": "호스트 주소는 비워 둘 수 없습니다", "es": "la dirección del host no debe estar vacía", "fr": "l'adresse de l'hôte ne doit pas être vide", "de": "Host-Adresse darf nicht leer sein",
		},
		"仓库地址不能为空": {
			"zh-TW": "倉庫位址不能為空", "en": "repository URL must not be empty", "ja": "リポジトリ URL は空にできません",
			"ko": "저장소 주소는 비워 둘 수 없습니다", "es": "la URL del repositorio no debe estar vacía", "fr": "l'URL du dépôt ne doit pas être vide", "de": "Repository-URL darf nicht leer sein",
		},
		"会话已过期,请重新登录": {
			"zh-TW": "工作階段已過期,請重新登入", "en": "session expired, please log in again", "ja": "セッションが期限切れです。再度ログインしてください",
			"ko": "세션이 만료되었습니다. 다시 로그인하세요", "es": "la sesión ha expirado, inicie sesión de nuevo", "fr": "session expirée, veuillez vous reconnecter", "de": "Sitzung abgelaufen, bitte erneut anmelden",
		},
		"保险库未配置 master key,无法加密或读取敏感字段": {
			"zh-TW": "保險庫未設定 master key,無法加密或讀取敏感欄位", "en": "vault has no master key configured; cannot encrypt or read sensitive fields", "ja": "vault に master key が設定されていないため、機密フィールドを暗号化または読み取りできません",
			"ko": "vault에 master key가 구성되지 않아 민감한 필드를 암호화하거나 읽을 수 없습니다", "es": "la bóveda no tiene master key configurada; no se pueden cifrar ni leer campos sensibles", "fr": "le coffre-fort n'a pas de master key configurée ; impossible de chiffrer ou de lire les champs sensibles", "de": "Vault hat keinen master key konfiguriert; sensible Felder können nicht verschlüsselt oder gelesen werden",
		},
		"保险库未配置 master key,无法生成或读取 webhook 签名密钥": {
			"zh-TW": "保險庫未設定 master key,無法產生或讀取 webhook 簽章金鑰", "en": "vault has no master key configured; cannot generate or read webhook signing key", "ja": "vault に master key が設定されていないため、webhook 署名キーを生成または読み取りできません",
			"ko": "vault에 master key가 구성되지 않아 webhook 서명 키를 생성하거나 읽을 수 없습니다", "es": "la bóveda no tiene master key configurada; no se puede generar ni leer la clave de firma del webhook", "fr": "le coffre-fort n'a pas de master key configurée ; impossible de générer ou de lire la clé de signature du webhook", "de": "Vault hat keinen master key konfiguriert; webhook-Signaturschlüssel kann nicht generiert oder gelesen werden",
		},
		"分支模式不能为空且需为合法通配(如 main、release/*)": {
			"zh-TW": "分支模式不能為空且需為合法萬用字元(如 main、release/*)", "en": "branch pattern must not be empty and must be a valid glob (e.g. main, release/*)", "ja": "ブランチパターンは空にできず、有効な glob である必要があります(例:main、release/*)",
			"ko": "브랜치 패턴은 비워 둘 수 없으며 유효한 glob이어야 합니다 (예: main, release/*)", "es": "el patrón de rama no debe estar vacío y debe ser un glob válido (p. ej. main, release/*)", "fr": "le motif de branche ne doit pas être vide et doit être un glob valide (p. ex. main, release/*)", "de": "Branch-Muster darf nicht leer sein und muss ein gültiges glob sein (z. B. main, release/*)",
		},
		"参数配置服务未初始化": {
			"zh-TW": "參數設定服務未初始化", "en": "parameter configuration service not initialized", "ja": "パラメータ設定サービスが初期化されていません",
			"ko": "매개변수 구성 서비스가 초기화되지 않았습니다", "es": "el servicio de configuración de parámetros no está inicializado", "fr": "le service de configuration des paramètres n'est pas initialisé", "de": "Parameter-Konfigurationsdienst nicht initialisiert",
		},
		"变量组名已被占用": {
			"zh-TW": "變數組名已被佔用", "en": "variable group name already in use", "ja": "変数グループ名は既に使用されています",
			"ko": "변수 그룹 이름이 이미 사용 중입니다", "es": "el nombre del grupo de variables ya está en uso", "fr": "le nom du groupe de variables est déjà utilisé", "de": "Name der Variablengruppe wird bereits verwendet",
		},
		"启用定时触发须填写 cron 表达式": {
			"zh-TW": "啟用定時觸發須填寫 cron 運算式", "en": "a cron expression is required to enable scheduled triggering", "ja": "スケジュールトリガーを有効にするには cron 式を入力する必要があります",
			"ko": "예약 트리거를 활성화하려면 cron 표현식을 입력해야 합니다", "es": "se requiere una expresión cron para habilitar el disparo programado", "fr": "une expression cron est requise pour activer le déclenchement planifié", "de": "Für die Aktivierung der geplanten Auslösung ist ein cron-Ausdruck erforderlich",
		},
		"容器名非法": {
			"zh-TW": "容器名稱非法", "en": "invalid container name", "ja": "コンテナ名が無効です",
			"ko": "잘못된 컨테이너 이름입니다", "es": "nombre de contenedor no válido", "fr": "nom de conteneur non valide", "de": "Ungültiger Containername",
		},
		"并发配置服务未初始化": {
			"zh-TW": "並行設定服務未初始化", "en": "concurrency configuration service not initialized", "ja": "並行設定サービスが初期化されていません",
			"ko": "동시성 구성 서비스가 초기화되지 않았습니다", "es": "el servicio de configuración de concurrencia no está inicializado", "fr": "le service de configuration de la concurrence n'est pas initialisé", "de": "Konfigurationsdienst für Nebenläufigkeit nicht initialisiert",
		},
		"指定的 runner 服务器不存在": {
			"zh-TW": "指定的 runner 伺服器不存在", "en": "the specified runner server does not exist", "ja": "指定された runner サーバーが存在しません",
			"ko": "지정된 runner 서버가 존재하지 않습니다", "es": "el servidor runner especificado no existe", "fr": "le serveur runner spécifié n'existe pas", "de": "Der angegebene runner-Server existiert nicht",
		},
		"无法读取仓库提交(仓库不可达或凭据无效)": {
			"zh-TW": "無法讀取倉庫提交(倉庫無法存取或憑證無效)", "en": "cannot read repository commits (repository unreachable or credential invalid)", "ja": "リポジトリのコミットを読み取れません(リポジトリに到達できないか認証情報が無効です)",
			"ko": "저장소 커밋을 읽을 수 없습니다 (저장소에 연결할 수 없거나 자격 증명이 잘못됨)", "es": "no se pueden leer los commits del repositorio (repositorio inaccesible o credencial no válida)", "fr": "impossible de lire les commits du dépôt (dépôt inaccessible ou identifiant non valide)", "de": "Repository-Commits können nicht gelesen werden (Repository nicht erreichbar oder Anmeldedaten ungültig)",
		},
		"服务器内部错误": {
			"zh-TW": "伺服器內部錯誤", "en": "internal server error", "ja": "サーバー内部エラー",
			"ko": "서버 내부 오류", "es": "error interno del servidor", "fr": "erreur interne du serveur", "de": "Interner Serverfehler",
		},
		"构建/部署配置服务未初始化": {
			"zh-TW": "建置/部署設定服務未初始化", "en": "build/deployment configuration service not initialized", "ja": "ビルド/デプロイ設定サービスが初期化されていません",
			"ko": "빌드/배포 구성 서비스가 초기화되지 않았습니다", "es": "el servicio de configuración de compilación/despliegue no está inicializado", "fr": "le service de configuration de build/déploiement n'est pas initialisé", "de": "Konfigurationsdienst für Build/Deployment nicht initialisiert",
		},
		"模板任务名与类型不能为空": {
			"zh-TW": "模板 Job 名稱與類型不能為空", "en": "template Job name and kind must not be empty", "ja": "テンプレートの Job 名と種別は空にできません",
			"ko": "템플릿 Job 이름과 종류는 비워 둘 수 없습니다", "es": "el nombre y el tipo del Job de la plantilla no deben estar vacíos", "fr": "le nom et le type du Job du Template ne doivent pas être vides", "de": "Name und Typ des Template-Jobs dürfen nicht leer sein",
		},
		"模板阶段非法(阶段名空 / kind 非枚举 / 须恰一个源阶段)": {
			"zh-TW": "模板階段非法(階段名空 / kind 非列舉 / 須恰一個來源階段)", "en": "invalid template Stage (empty Stage name / kind not in enum / exactly one source Stage required)", "ja": "テンプレートのステージが無効です(ステージ名が空 / kind が列挙値でない / 起点ステージはちょうど 1 つ必要)",
			"ko": "잘못된 템플릿 스테이지 (스테이지 이름 비어 있음 / kind가 열거값이 아님 / 소스 스테이지는 정확히 하나 필요)", "es": "Etapa de plantilla no válida (nombre de Etapa vacío / kind no enumerado / se requiere exactamente una Etapa de origen)", "fr": "Étape de Template non valide (nom d'Étape vide / kind hors énumération / exactement une Étape source requise)", "de": "Ungültige Template-Phase (Phasenname leer / kind nicht im Enum / genau eine Quellphase erforderlich)",
		},
		"渠道配置不完整或非法": {
			"zh-TW": "通道設定不完整或非法", "en": "channel configuration incomplete or invalid", "ja": "チャネル設定が不完全または無効です",
			"ko": "채널 구성이 불완전하거나 잘못되었습니다", "es": "la configuración del canal está incompleta o no es válida", "fr": "la configuration du canal est incomplète ou non valide", "de": "Kanalkonfiguration unvollständig oder ungültig",
		},
		"环境名不能为空,镜像仓库类型须为 harbor/acr/dockerhub/custom": {
			"zh-TW": "環境名不能為空,鏡像倉庫類型須為 harbor/acr/dockerhub/custom", "en": "environment name must not be empty; image registry type must be harbor/acr/dockerhub/custom", "ja": "環境名は空にできず、イメージレジストリの種別は harbor/acr/dockerhub/custom である必要があります",
			"ko": "환경 이름은 비워 둘 수 없으며, 이미지 레지스트리 유형은 harbor/acr/dockerhub/custom이어야 합니다", "es": "el nombre del entorno no debe estar vacío; el tipo de registro de imágenes debe ser harbor/acr/dockerhub/custom", "fr": "le nom de l'environnement ne doit pas être vide ; le type de registre d'images doit être harbor/acr/dockerhub/custom", "de": "Umgebungsname darf nicht leer sein; Image-Registry-Typ muss harbor/acr/dockerhub/custom sein",
		},
		"用户名或口令错误": {
			"zh-TW": "使用者名稱或密碼錯誤", "en": "invalid username or password", "ja": "ユーザー名またはパスワードが正しくありません",
			"ko": "사용자 이름 또는 비밀번호가 올바르지 않습니다", "es": "nombre de usuario o contraseña incorrectos", "fr": "nom d'utilisateur ou mot de passe incorrect", "de": "Benutzername oder Passwort falsch",
		},
		"缺少 stageId": {
			"zh-TW": "缺少 stageId", "en": "missing stageId", "ja": "stageId がありません",
			"ko": "stageId가 없습니다", "es": "falta stageId", "fr": "stageId manquant", "de": "stageId fehlt",
		},
		"自定义节点名已被占用": {
			"zh-TW": "自訂節點名已被佔用", "en": "custom node name already in use", "ja": "カスタムノード名は既に使用されています",
			"ko": "사용자 지정 노드 이름이 이미 사용 중입니다", "es": "el nombre del nodo personalizado ya está en uso", "fr": "le nom du nœud personnalisé est déjà utilisé", "de": "Name des benutzerdefinierten Knotens wird bereits verwendet",
		},
		"触发配置服务未初始化": {
			"zh-TW": "觸發設定服務未初始化", "en": "trigger configuration service not initialized", "ja": "トリガー設定サービスが初期化されていません",
			"ko": "트리거 구성 서비스가 초기화되지 않았습니다", "es": "el servicio de configuración de disparadores no está inicializado", "fr": "le service de configuration des déclencheurs n'est pas initialisé", "de": "Trigger-Konfigurationsdienst nicht initialisiert",
		},
		"该 provider 需要配置 client secret": {
			"zh-TW": "該 provider 需要設定 client secret", "en": "this provider requires a client secret to be configured", "ja": "この provider には client secret の設定が必要です",
			"ko": "이 provider에는 client secret 구성이 필요합니다", "es": "este provider requiere configurar un client secret", "fr": "ce provider nécessite la configuration d'un client secret", "de": "Dieser provider erfordert die Konfiguration eines client secret",
		},
		"该运行尚无 AI 诊断,无法反馈": {
			"zh-TW": "該執行尚無 AI 診斷,無法回饋", "en": "this run has no AI diagnosis yet; cannot submit feedback", "ja": "この実行にはまだ AI 診断がないため、フィードバックできません",
			"ko": "이 실행에는 아직 AI 진단이 없어 피드백할 수 없습니다", "es": "esta ejecución aún no tiene diagnóstico de IA; no se puede enviar comentarios", "fr": "cette exécution n'a pas encore de diagnostic IA ; impossible d'envoyer un retour", "de": "Diese Ausführung hat noch keine KI-Diagnose; Feedback nicht möglich",
		},
		"该运行阶段当前不在等待审批": {
			"zh-TW": "該執行階段目前不在等待審批", "en": "this run stage is not currently awaiting approval", "ja": "この実行ステージは現在承認待ちではありません",
			"ko": "이 실행 스테이지는 현재 승인 대기 중이 아닙니다", "es": "esta etapa de la ejecución no está actualmente a la espera de aprobación", "fr": "cette étape de l'exécution n'est pas actuellement en attente d'approbation", "de": "Diese Ausführungsphase wartet derzeit nicht auf eine Genehmigung",
		},
		"请提供要解释的命令": {
			"zh-TW": "請提供要解釋的命令", "en": "please provide the command to explain", "ja": "説明するコマンドを指定してください",
			"ko": "설명할 명령을 입력하세요", "es": "proporcione el comando que se va a explicar", "fr": "veuillez fournir la commande à expliquer", "de": "Bitte geben Sie den zu erklärenden Befehl an",
		},
		"请选择仓库凭据": {
			"zh-TW": "請選擇倉庫憑證", "en": "please select a repository credential", "ja": "リポジトリの認証情報を選択してください",
			"ko": "저장소 자격 증명을 선택하세요", "es": "seleccione una credencial del repositorio", "fr": "veuillez sélectionner un identifiant de dépôt", "de": "Bitte wählen Sie Repository-Anmeldedaten aus",
		},
		"运行不存在": {
			"zh-TW": "執行不存在", "en": "run not found", "ja": "実行が見つかりません",
			"ko": "실행을 찾을 수 없습니다", "es": "ejecución no encontrada", "fr": "exécution introuvable", "de": "Ausführung nicht gefunden",
		},
		"通知模板不存在": {
			"zh-TW": "通知模板不存在", "en": "notification template not found", "ja": "通知テンプレートが見つかりません",
			"ko": "알림 템플릿을 찾을 수 없습니다", "es": "plantilla de notificación no encontrada", "fr": "modèle de notification introuvable", "de": "Benachrichtigungsvorlage nicht gefunden",
		},
		"阶段或任务 id 重复": {
			"zh-TW": "階段或 Job id 重複", "en": "duplicate Stage or Job id", "ja": "ステージまたは Job の id が重複しています",
			"ko": "스테이지 또는 Job id가 중복되었습니다", "es": "id de Etapa o Job duplicado", "fr": "id d'Étape ou de Job en double", "de": "Doppelte Stage- oder Job-id",
		},
		"项目名非法(仅字母数字与 . _ -,不以 - 开头)": {
			"zh-TW": "專案名非法(僅字母數字與 . _ -,不以 - 開頭)", "en": "invalid Project name (only alphanumerics and . _ -, must not start with -)", "ja": "プロジェクト名が無効です(英数字と . _ - のみ、- で始めることはできません)",
			"ko": "잘못된 프로젝트 이름입니다 (영숫자와 . _ -만 허용, -로 시작할 수 없음)", "es": "nombre de Proyecto no válido (solo caracteres alfanuméricos y . _ -, no debe comenzar con -)", "fr": "nom de Projet non valide (uniquement alphanumériques et . _ -, ne doit pas commencer par -)", "de": "Ungültiger Projektname (nur alphanumerische Zeichen und . _ -, darf nicht mit - beginnen)",
		},
	})
}
