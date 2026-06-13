package i18n

func init() {
	register(map[string]map[string]string{
		"AI 生成所需服务未初始化": {
			"zh-TW": "AI 生成所需服務未初始化", "en": "service required for AI generation is not initialized", "ja": "AI 生成に必要なサービスが初期化されていません",
			"ko": "AI 생성에 필요한 서비스가 초기화되지 않았습니다", "es": "el servicio requerido para la generación con IA no está inicializado", "fr": "le service requis pour la génération par IA n'est pas initialisé", "de": "der für die KI-Generierung erforderliche Dienst ist nicht initialisiert",
		},
		"blob 需指定文件路径": {
			"zh-TW": "blob 需指定檔案路徑", "en": "blob requires a file path", "ja": "blob にはファイルパスの指定が必要です",
			"ko": "blob에는 파일 경로를 지정해야 합니다", "es": "blob requiere una ruta de archivo", "fr": "blob nécessite un chemin de fichier", "de": "blob erfordert einen Dateipfad",
		},
		"metric 必须是 cpu / memory / disk 之一": {
			"zh-TW": "metric 必須是 cpu / memory / disk 之一", "en": "metric must be one of cpu / memory / disk", "ja": "metric は cpu / memory / disk のいずれかである必要があります",
			"ko": "metric은 cpu / memory / disk 중 하나여야 합니다", "es": "metric debe ser uno de cpu / memory / disk", "fr": "metric doit être l'un de cpu / memory / disk", "de": "metric muss eines von cpu / memory / disk sein",
		},
		"provider 只能为 claude、openai 或 ollama": {
			"zh-TW": "provider 只能為 claude、openai 或 ollama", "en": "provider must be claude, openai or ollama", "ja": "provider は claude、openai または ollama のみ指定できます",
			"ko": "provider는 claude, openai 또는 ollama만 가능합니다", "es": "provider solo puede ser claude, openai u ollama", "fr": "provider ne peut être que claude, openai ou ollama", "de": "provider darf nur claude, openai oder ollama sein",
		},
		"verdict 只能为 up 或 down": {
			"zh-TW": "verdict 只能為 up 或 down", "en": "verdict must be up or down", "ja": "verdict は up または down のみ指定できます",
			"ko": "verdict는 up 또는 down만 가능합니다", "es": "verdict solo puede ser up o down", "fr": "verdict ne peut être que up ou down", "de": "verdict darf nur up oder down sein",
		},
		"window 必须形如 30d / 7d / 90d,且不超过 730 天": {
			"zh-TW": "window 必須形如 30d / 7d / 90d,且不超過 730 天", "en": "window must be in the form 30d / 7d / 90d and must not exceed 730 days", "ja": "window は 30d / 7d / 90d の形式で、730 日を超えてはなりません",
			"ko": "window는 30d / 7d / 90d 형식이어야 하며 730일을 초과할 수 없습니다", "es": "window debe tener el formato 30d / 7d / 90d y no debe superar los 730 días", "fr": "window doit être de la forme 30d / 7d / 90d et ne doit pas dépasser 730 jours", "de": "window muss das Format 30d / 7d / 90d haben und darf 730 Tage nicht überschreiten",
		},
		"不可跳级:只能晋级到下一环境": {
			"zh-TW": "不可跳級:只能晉級到下一環境", "en": "cannot skip levels: promotion is only allowed to the next environment", "ja": "段階を飛ばすことはできません:次の環境にのみ昇格できます",
			"ko": "단계를 건너뛸 수 없습니다: 다음 환경으로만 승격할 수 있습니다", "es": "no se pueden saltar niveles: solo se permite promover al siguiente entorno", "fr": "impossible de sauter des niveaux : la promotion n'est autorisée que vers l'environnement suivant", "de": "Stufen können nicht übersprungen werden: Eine Beförderung ist nur in die nächste Umgebung möglich",
		},
		"仓库地址不可达,请检查地址": {
			"zh-TW": "倉庫地址不可達,請檢查地址", "en": "repository address is unreachable, please check the address", "ja": "リポジトリのアドレスに到達できません。アドレスを確認してください",
			"ko": "저장소 주소에 연결할 수 없습니다. 주소를 확인하세요", "es": "no se puede acceder a la dirección del repositorio, compruebe la dirección", "fr": "l'adresse du dépôt est inaccessible, veuillez vérifier l'adresse", "de": "Repository-Adresse ist nicht erreichbar, bitte überprüfen Sie die Adresse",
		},
		"会话不存在": {
			"zh-TW": "會話不存在", "en": "session not found", "ja": "セッションが存在しません",
			"ko": "세션을 찾을 수 없습니다", "es": "sesión no encontrada", "fr": "session introuvable", "de": "Sitzung nicht gefunden",
		},
		"保险库未配置 master key,无法加密或读取 client secret": {
			"zh-TW": "保險庫未配置 master key,無法加密或讀取 client secret", "en": "vault has no master key configured, cannot encrypt or read client secret", "ja": "ボールトに master key が設定されていないため、client secret を暗号化または読み取りできません",
			"ko": "볼트에 master key가 구성되지 않아 client secret을 암호화하거나 읽을 수 없습니다", "es": "el almacén no tiene una master key configurada, no se puede cifrar ni leer el client secret", "fr": "le coffre n'a pas de master key configurée, impossible de chiffrer ou de lire le client secret", "de": "im Tresor ist kein master key konfiguriert, client secret kann nicht verschlüsselt oder gelesen werden",
		},
		"保险库未配置 master key,无法校验或引用 secret 凭据": {
			"zh-TW": "保險庫未配置 master key,無法校驗或引用 secret 憑據", "en": "vault has no master key configured, cannot verify or reference secret credential", "ja": "ボールトに master key が設定されていないため、secret 認証情報を検証または参照できません",
			"ko": "볼트에 master key가 구성되지 않아 secret 자격 증명을 검증하거나 참조할 수 없습니다", "es": "el almacén no tiene una master key configurada, no se puede verificar ni referenciar la credencial secret", "fr": "le coffre n'a pas de master key configurée, impossible de vérifier ou de référencer l'identifiant secret", "de": "im Tresor ist kein master key konfiguriert, secret-Anmeldedaten können nicht überprüft oder referenziert werden",
		},
		"凭据类型非法": {
			"zh-TW": "憑據類型非法", "en": "invalid credential type", "ja": "認証情報のタイプが無効です",
			"ko": "자격 증명 유형이 잘못되었습니다", "es": "tipo de credencial no válido", "fr": "type d'identifiant non valide", "de": "ungültiger Anmeldedatentyp",
		},
		"参数校验失败": {
			"zh-TW": "參數校驗失敗", "en": "parameter validation failed", "ja": "パラメータの検証に失敗しました",
			"ko": "매개변수 검증에 실패했습니다", "es": "error en la validación de parámetros", "fr": "échec de la validation des paramètres", "de": "Parametervalidierung fehlgeschlagen",
		},
		"变量组不存在": {
			"zh-TW": "變數組不存在", "en": "variable group not found", "ja": "変数グループが存在しません",
			"ko": "변수 그룹을 찾을 수 없습니다", "es": "grupo de variables no encontrado", "fr": "groupe de variables introuvable", "de": "Variablengruppe nicht gefunden",
		},
		"名称非法(仅字母数字与 . _ -,不以 - 开头)": {
			"zh-TW": "名稱非法(僅字母數字與 . _ -,不以 - 開頭)", "en": "invalid name (only alphanumerics and . _ -, must not start with -)", "ja": "名前が無効です(英数字と . _ - のみ、- で始めることはできません)",
			"ko": "이름이 잘못되었습니다(영숫자와 . _ -만 허용, -로 시작할 수 없음)", "es": "nombre no válido (solo alfanuméricos y . _ -, no debe comenzar con -)", "fr": "nom non valide (uniquement alphanumériques et . _ -, ne doit pas commencer par -)", "de": "ungültiger Name (nur alphanumerisch und . _ -, darf nicht mit - beginnen)",
		},
		"审计服务未初始化": {
			"zh-TW": "審計服務未初始化", "en": "audit service not initialized", "ja": "監査サービスが初期化されていません",
			"ko": "감사 서비스가 초기화되지 않았습니다", "es": "el servicio de auditoría no está inicializado", "fr": "le service d'audit n'est pas initialisé", "de": "Audit-Dienst ist nicht initialisiert",
		},
		"并发上限非法(须为 0..64 的整数;0 表示不限项目级)": {
			"zh-TW": "並發上限非法(須為 0..64 的整數;0 表示不限專案級)", "en": "invalid concurrency limit (must be an integer in 0..64; 0 means no project-level limit)", "ja": "並行数の上限が無効です(0..64 の整数である必要があります;0 はプロジェクトレベルの制限なしを意味します)",
			"ko": "동시 실행 상한이 잘못되었습니다(0..64 범위의 정수여야 함; 0은 프로젝트 수준 제한 없음을 의미)", "es": "límite de concurrencia no válido (debe ser un entero en 0..64; 0 significa sin límite a nivel de proyecto)", "fr": "limite de concurrence non valide (doit être un entier dans 0..64 ; 0 signifie aucune limite au niveau du projet)", "de": "ungültige Parallelitätsgrenze (muss eine ganze Zahl in 0..64 sein; 0 bedeutet keine projektweite Begrenzung)",
		},
		"当前口令错误": {
			"zh-TW": "目前口令錯誤", "en": "current password is incorrect", "ja": "現在のパスワードが正しくありません",
			"ko": "현재 비밀번호가 올바르지 않습니다", "es": "la contraseña actual es incorrecta", "fr": "le mot de passe actuel est incorrect", "de": "das aktuelle Passwort ist falsch",
		},
		"无法读取仓库分支(仓库不可达或凭据无效)": {
			"zh-TW": "無法讀取倉庫分支(倉庫不可達或憑據無效)", "en": "cannot read repository branches (repository unreachable or invalid credential)", "ja": "リポジトリのブランチを読み取れません(リポジトリに到達できないか、認証情報が無効です)",
			"ko": "저장소 브랜치를 읽을 수 없습니다(저장소에 연결할 수 없거나 자격 증명이 유효하지 않음)", "es": "no se pueden leer las ramas del repositorio (repositorio inaccesible o credencial no válida)", "fr": "impossible de lire les branches du dépôt (dépôt inaccessible ou identifiant non valide)", "de": "Repository-Branches können nicht gelesen werden (Repository nicht erreichbar oder ungültige Anmeldedaten)",
		},
		"服务器不支持流式响应": {
			"zh-TW": "伺服器不支援串流回應", "en": "server does not support streaming responses", "ja": "サーバーはストリーミング応答をサポートしていません",
			"ko": "서버가 스트리밍 응답을 지원하지 않습니다", "es": "el servidor no admite respuestas en streaming", "fr": "le serveur ne prend pas en charge les réponses en streaming", "de": "der Server unterstützt keine Streaming-Antworten",
		},
		"未匹配策略非法": {
			"zh-TW": "未匹配策略非法", "en": "invalid no-match policy", "ja": "未マッチ時のポリシーが無効です",
			"ko": "미일치 정책이 잘못되었습니다", "es": "política de no coincidencia no válida", "fr": "politique de non-correspondance non valide", "de": "ungültige Nicht-Treffer-Richtlinie",
		},
		"模板不存在": {
			"zh-TW": "範本不存在", "en": "template not found", "ja": "テンプレートが存在しません",
			"ko": "템플릿을 찾을 수 없습니다", "es": "plantilla no encontrada", "fr": "modèle introuvable", "de": "Vorlage nicht gefunden",
		},
		"模板阶段或任务 id 重复": {
			"zh-TW": "範本階段或任務 id 重複", "en": "duplicate stage or job id in template", "ja": "テンプレート内のステージまたはジョブの id が重複しています",
			"ko": "템플릿의 스테이지 또는 잡 id가 중복됩니다", "es": "id de etapa o job duplicado en la plantilla", "fr": "id d'étape ou de job en double dans le modèle", "de": "doppelte Phasen- oder Job-id in der Vorlage",
		},
		"渠道类型只能为 webhook、email、wecom、dingtalk 或 feishu": {
			"zh-TW": "通道類型只能為 webhook、email、wecom、dingtalk 或 feishu", "en": "channel type must be webhook, email, wecom, dingtalk or feishu", "ja": "チャネルタイプは webhook、email、wecom、dingtalk または feishu のみ指定できます",
			"ko": "채널 유형은 webhook, email, wecom, dingtalk 또는 feishu만 가능합니다", "es": "el tipo de canal solo puede ser webhook, email, wecom, dingtalk o feishu", "fr": "le type de canal ne peut être que webhook, email, wecom, dingtalk ou feishu", "de": "der Kanaltyp darf nur webhook, email, wecom, dingtalk oder feishu sein",
		},
		"环境内变量 key 重复": {
			"zh-TW": "環境內變數 key 重複", "en": "duplicate variable key within environment", "ja": "環境内で変数の key が重複しています",
			"ko": "환경 내 변수 key가 중복됩니다", "es": "clave de variable duplicada dentro del entorno", "fr": "clé de variable en double dans l'environnement", "de": "doppelter Variablen-key innerhalb der Umgebung",
		},
		"环境链含重复环境名": {
			"zh-TW": "環境鏈含重複環境名", "en": "environment chain contains duplicate environment names", "ja": "環境チェーンに重複する環境名が含まれています",
			"ko": "환경 체인에 중복된 환경 이름이 있습니다", "es": "la cadena de entornos contiene nombres de entorno duplicados", "fr": "la chaîne d'environnements contient des noms d'environnement en double", "de": "die Umgebungskette enthält doppelte Umgebungsnamen",
		},
		"端口必须在 1..65535 之间": {
			"zh-TW": "連接埠必須在 1..65535 之間", "en": "port must be in the range 1..65535", "ja": "ポートは 1..65535 の範囲内である必要があります",
			"ko": "포트는 1..65535 범위 내여야 합니다", "es": "el puerto debe estar en el rango 1..65535", "fr": "le port doit être compris dans la plage 1..65535", "de": "der Port muss im Bereich 1..65535 liegen",
		},
		"自定义节点不存在": {
			"zh-TW": "自訂節點不存在", "en": "custom node not found", "ja": "カスタムノードが存在しません",
			"ko": "사용자 정의 노드를 찾을 수 없습니다", "es": "nodo personalizado no encontrado", "fr": "nœud personnalisé introuvable", "de": "benutzerdefinierter Knoten nicht gefunden",
		},
		"触发配置不存在": {
			"zh-TW": "觸發設定不存在", "en": "trigger configuration not found", "ja": "トリガー設定が存在しません",
			"ko": "트리거 구성을 찾을 수 없습니다", "es": "configuración de disparador no encontrada", "fr": "configuration de déclencheur introuvable", "de": "Trigger-Konfiguration nicht gefunden",
		},
		"该 provider 需要配置 API 密钥": {
			"zh-TW": "該 provider 需要設定 API 密鑰", "en": "this provider requires an API key to be configured", "ja": "この provider には API キーの設定が必要です",
			"ko": "이 provider는 API 키 구성이 필요합니다", "es": "este provider requiere configurar una clave de API", "fr": "ce provider nécessite la configuration d'une clé API", "de": "dieser provider erfordert die Konfiguration eines API-Schlüssels",
		},
		"该运行下不存在指定产物": {
			"zh-TW": "該執行下不存在指定產物", "en": "the specified artifact does not exist for this run", "ja": "この実行には指定されたアーティファクトが存在しません",
			"ko": "이 실행에 지정된 아티팩트가 존재하지 않습니다", "es": "el artefacto especificado no existe para esta ejecución", "fr": "l'artefact spécifié n'existe pas pour cette exécution", "de": "das angegebene Artefakt existiert für diese Ausführung nicht",
		},
		"该运行没有暂停待续发的目标": {
			"zh-TW": "該執行沒有暫停待續發的目標", "en": "this run has no paused target awaiting resumption", "ja": "この実行には再開を待っている一時停止中のターゲットがありません",
			"ko": "이 실행에는 재개를 기다리는 일시 중지된 대상이 없습니다", "es": "esta ejecución no tiene ningún destino en pausa pendiente de reanudación", "fr": "cette exécution n'a aucune cible en pause en attente de reprise", "de": "diese Ausführung hat kein pausiertes Ziel, das auf Fortsetzung wartet",
		},
		"请描述你想要的 compose": {
			"zh-TW": "請描述你想要的 compose", "en": "please describe the compose you want", "ja": "希望する compose を記述してください",
			"ko": "원하는 compose를 설명하세요", "es": "describa el compose que desea", "fr": "veuillez décrire le compose souhaité", "de": "bitte beschreiben Sie das gewünschte compose",
		},
		"请选择 SSH 凭据": {
			"zh-TW": "請選擇 SSH 憑據", "en": "please select an SSH credential", "ja": "SSH 認証情報を選択してください",
			"ko": "SSH 자격 증명을 선택하세요", "es": "seleccione una credencial SSH", "fr": "veuillez sélectionner un identifiant SSH", "de": "bitte wählen Sie SSH-Anmeldedaten aus",
		},
		"路由引用的通知渠道不存在": {
			"zh-TW": "路由引用的通知通道不存在", "en": "notification channel referenced by route not found", "ja": "ルーティングが参照する通知チャネルが存在しません",
			"ko": "라우팅이 참조하는 알림 채널을 찾을 수 없습니다", "es": "el canal de notificación referenciado por la ruta no existe", "fr": "le canal de notification référencé par la route est introuvable", "de": "der von der Route referenzierte Benachrichtigungskanal wurde nicht gefunden",
		},
		"通知服务未初始化": {
			"zh-TW": "通知服務未初始化", "en": "notification service not initialized", "ja": "通知サービスが初期化されていません",
			"ko": "알림 서비스가 초기화되지 않았습니다", "es": "el servicio de notificaciones no está inicializado", "fr": "le service de notification n'est pas initialisé", "de": "Benachrichtigungsdienst ist nicht initialisiert",
		},
		"阶段名不能为空且 kind 必须为 source/build/deploy/notify/custom 之一": {
			"zh-TW": "階段名不能為空且 kind 必須為 source/build/deploy/notify/custom 之一", "en": "stage name must not be empty and kind must be one of source/build/deploy/notify/custom", "ja": "ステージ名は空にできず、kind は source/build/deploy/notify/custom のいずれかである必要があります",
			"ko": "스테이지 이름은 비워둘 수 없으며 kind는 source/build/deploy/notify/custom 중 하나여야 합니다", "es": "el nombre de la etapa no debe estar vacío y kind debe ser uno de source/build/deploy/notify/custom", "fr": "le nom de l'étape ne doit pas être vide et kind doit être l'un de source/build/deploy/notify/custom", "de": "der Phasenname darf nicht leer sein und kind muss eines von source/build/deploy/notify/custom sein",
		},
		"项目名非法": {
			"zh-TW": "專案名非法", "en": "invalid project name", "ja": "プロジェクト名が無効です",
			"ko": "프로젝트 이름이 잘못되었습니다", "es": "nombre de proyecto no válido", "fr": "nom de projet non valide", "de": "ungültiger Projektname",
		},
	})
}
