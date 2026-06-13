package i18n

func init() {
	register(map[string]map[string]string{
		"AI 助手未初始化": {
			"zh-TW": "AI 助手未初始化", "en": "AI assistant service not initialized", "ja": "AI アシスタントが初期化されていません",
			"ko": "AI 어시스턴트가 초기화되지 않았습니다", "es": "El asistente de IA no está inicializado",
			"fr": "Assistant IA non initialisé", "de": "KI-Assistent nicht initialisiert",
		},
		"CSRF token 缺失或不匹配": {
			"zh-TW": "CSRF token 缺失或不符", "en": "CSRF token missing or mismatched", "ja": "CSRF token がないか一致しません",
			"ko": "CSRF token이 없거나 일치하지 않습니다", "es": "Falta el CSRF token o no coincide",
			"fr": "CSRF token manquant ou non concordant", "de": "CSRF token fehlt oder stimmt nicht überein",
		},
		"compose 内容不能为空": {
			"zh-TW": "compose 內容不能為空", "en": "compose content must not be empty", "ja": "compose の内容を空にすることはできません",
			"ko": "compose 내용은 비워 둘 수 없습니다", "es": "El contenido de compose no puede estar vacío",
			"fr": "Le contenu compose ne doit pas être vide", "de": "compose-Inhalt darf nicht leer sein",
		},
		"operator 必须是 gt / lt 之一": {
			"zh-TW": "operator 必須是 gt / lt 之一", "en": "operator must be one of gt / lt", "ja": "operator は gt / lt のいずれかである必要があります",
			"ko": "operator는 gt / lt 중 하나여야 합니다", "es": "operator debe ser gt o lt",
			"fr": "operator doit être gt ou lt", "de": "operator muss gt oder lt sein",
		},
		"runner 配置服务未初始化": {
			"zh-TW": "runner 設定服務未初始化", "en": "runner config service not initialized", "ja": "runner 設定サービスが初期化されていません",
			"ko": "runner 설정 서비스가 초기화되지 않았습니다", "es": "El servicio de configuración de runner no está inicializado",
			"fr": "Service de configuration runner non initialisé", "de": "runner-Konfigurationsdienst nicht initialisiert",
		},
		"webhook 接收服务未初始化": {
			"zh-TW": "webhook 接收服務未初始化", "en": "webhook receiver service not initialized", "ja": "webhook 受信サービスが初期化されていません",
			"ko": "webhook 수신 서비스가 초기화되지 않았습니다", "es": "El servicio de recepción de webhook no está inicializado",
			"fr": "Service de réception webhook non initialisé", "de": "webhook-Empfangsdienst nicht initialisiert",
		},
		"下游目标不能是上游项目自身(自环)": {
			"zh-TW": "下游目標不能是上游專案自身(自環)", "en": "Downstream target cannot be the upstream project itself (self-loop)", "ja": "下流ターゲットを上流プロジェクト自身にすることはできません(自己ループ)",
			"ko": "다운스트림 대상은 업스트림 프로젝트 자신이 될 수 없습니다(자기 루프)", "es": "El destino descendente no puede ser el propio proyecto ascendente (bucle propio)",
			"fr": "La cible en aval ne peut pas être le projet en amont lui-même (boucle réflexive)", "de": "Das Downstream-Ziel darf nicht das Upstream-Projekt selbst sein (Selbstschleife)",
		},
		"事件类型非法:只能为 build_succeeded、build_failed、deploy_succeeded、deploy_failed、rollback 或 health_check_failed": {
			"zh-TW": "事件類型非法:只能為 build_succeeded、build_failed、deploy_succeeded、deploy_failed、rollback 或 health_check_failed", "en": "Invalid event type: must be one of build_succeeded, build_failed, deploy_succeeded, deploy_failed, rollback or health_check_failed", "ja": "イベントタイプが不正です:build_succeeded、build_failed、deploy_succeeded、deploy_failed、rollback または health_check_failed のいずれかである必要があります",
			"ko": "이벤트 유형이 잘못되었습니다: build_succeeded, build_failed, deploy_succeeded, deploy_failed, rollback 또는 health_check_failed 중 하나여야 합니다", "es": "Tipo de evento no válido: debe ser build_succeeded, build_failed, deploy_succeeded, deploy_failed, rollback o health_check_failed",
			"fr": "Type d'événement invalide : doit être build_succeeded, build_failed, deploy_succeeded, deploy_failed, rollback ou health_check_failed", "de": "Ungültiger Ereignistyp: muss build_succeeded, build_failed, deploy_succeeded, deploy_failed, rollback oder health_check_failed sein",
		},
		"代码管理区未启用,无法列分支": {
			"zh-TW": "程式碼管理區未啟用,無法列出分支", "en": "Source management is not enabled; cannot list branches", "ja": "コード管理エリアが有効でないため、ブランチを一覧表示できません",
			"ko": "코드 관리 영역이 활성화되지 않아 브랜치를 나열할 수 없습니다", "es": "La gestión de código no está habilitada; no se pueden listar las ramas",
			"fr": "La gestion du code n'est pas activée ; impossible de lister les branches", "de": "Die Code-Verwaltung ist nicht aktiviert; Branches können nicht aufgelistet werden",
		},
		"保险库未配置": {
			"zh-TW": "保險庫未設定", "en": "Vault not configured", "ja": "Vault が設定されていません",
			"ko": "Vault가 구성되지 않았습니다", "es": "Vault no está configurado",
			"fr": "Vault non configuré", "de": "Vault nicht konfiguriert",
		},
		"保险库未配置 master key,无法取 SSH 凭据": {
			"zh-TW": "保險庫未設定 master key,無法取得 SSH 憑據", "en": "Vault has no master key configured; cannot retrieve SSH credential", "ja": "Vault に master key が設定されていないため、SSH 認証情報を取得できません",
			"ko": "Vault에 master key가 구성되지 않아 SSH 자격 증명을 가져올 수 없습니다", "es": "Vault no tiene una master key configurada; no se puede obtener la credencial SSH",
			"fr": "Aucune master key configurée dans Vault ; impossible de récupérer l'identifiant SSH", "de": "Im Vault ist kein master key konfiguriert; SSH-Anmeldedaten können nicht abgerufen werden",
		},
		"凭据不存在": {
			"zh-TW": "憑據不存在", "en": "Credential not found", "ja": "認証情報が見つかりません",
			"ko": "자격 증명을 찾을 수 없습니다", "es": "Credencial no encontrada",
			"fr": "Identifiant introuvable", "de": "Anmeldedaten nicht gefunden",
		},
		"分支模式非法": {
			"zh-TW": "分支模式非法", "en": "Invalid branch pattern", "ja": "ブランチパターンが不正です",
			"ko": "브랜치 패턴이 잘못되었습니다", "es": "Patrón de rama no válido",
			"fr": "Modèle de branche invalide", "de": "Ungültiges Branch-Muster",
		},
		"反馈服务未初始化": {
			"zh-TW": "回饋服務未初始化", "en": "Feedback service not initialized", "ja": "フィードバックサービスが初期化されていません",
			"ko": "피드백 서비스가 초기화되지 않았습니다", "es": "El servicio de comentarios no está inicializado",
			"fr": "Service de retour non initialisé", "de": "Feedback-Dienst nicht initialisiert",
		},
		"变量组服务未初始化": {
			"zh-TW": "變數組服務未初始化", "en": "Variable group service not initialized", "ja": "変数グループサービスが初期化されていません",
			"ko": "변수 그룹 서비스가 초기화되지 않았습니다", "es": "El servicio de grupos de variables no está inicializado",
			"fr": "Service de groupes de variables non initialisé", "de": "Variablengruppen-Dienst nicht initialisiert",
		},
		"回滚服务未初始化": {
			"zh-TW": "回滾服務未初始化", "en": "Rollback service not initialized", "ja": "ロールバックサービスが初期化されていません",
			"ko": "롤백 서비스가 초기화되지 않았습니다", "es": "El servicio de reversión no está inicializado",
			"fr": "Service de rollback non initialisé", "de": "Rollback-Dienst nicht initialisiert",
		},
		"尚未配置环境链": {
			"zh-TW": "尚未設定環境鏈", "en": "Environment chain not configured", "ja": "環境チェーンが設定されていません",
			"ko": "환경 체인이 구성되지 않았습니다", "es": "La cadena de entornos no está configurada",
			"fr": "Chaîne d'environnements non configurée", "de": "Umgebungskette nicht konfiguriert",
		},
		"异常检测服务未初始化": {
			"zh-TW": "異常偵測服務未初始化", "en": "Anomaly detection service not initialized", "ja": "異常検知サービスが初期化されていません",
			"ko": "이상 탐지 서비스가 초기화되지 않았습니다", "es": "El servicio de detección de anomalías no está inicializado",
			"fr": "Service de détection d'anomalies non initialisé", "de": "Anomalieerkennungsdienst nicht initialisiert",
		},
		"文件不存在": {
			"zh-TW": "檔案不存在", "en": "File not found", "ja": "ファイルが見つかりません",
			"ko": "파일을 찾을 수 없습니다", "es": "Archivo no encontrado",
			"fr": "Fichier introuvable", "de": "Datei nicht gefunden",
		},
		"晋级审批被拒绝": {
			"zh-TW": "晉級審批被拒絕", "en": "Promotion approval was rejected", "ja": "昇格の承認が却下されました",
			"ko": "승격 승인이 거부되었습니다", "es": "La aprobación de promoción fue rechazada",
			"fr": "L'approbation de promotion a été rejetée", "de": "Die Freigabe der Promotion wurde abgelehnt",
		},
		"服务器名称不能为空": {
			"zh-TW": "伺服器名稱不能為空", "en": "Server name must not be empty", "ja": "サーバー名を空にすることはできません",
			"ko": "서버 이름은 비워 둘 수 없습니다", "es": "El nombre del servidor no puede estar vacío",
			"fr": "Le nom du serveur ne doit pas être vide", "de": "Servername darf nicht leer sein",
		},
		"构建模型必须为 dockerfile/toolchain,产物类型必须为 image/jar/dist": {
			"zh-TW": "建置模型必須為 dockerfile/toolchain,產物類型必須為 image/jar/dist", "en": "Build model must be dockerfile/toolchain, and artifact type must be image/jar/dist", "ja": "ビルドモデルは dockerfile/toolchain、成果物タイプは image/jar/dist である必要があります",
			"ko": "빌드 모델은 dockerfile/toolchain이어야 하고, 산출물 유형은 image/jar/dist여야 합니다", "es": "El modelo de compilación debe ser dockerfile/toolchain y el tipo de artefacto debe ser image/jar/dist",
			"fr": "Le modèle de build doit être dockerfile/toolchain et le type d'artefact doit être image/jar/dist", "de": "Das Build-Modell muss dockerfile/toolchain sein und der Artefakttyp muss image/jar/dist sein",
		},
		"模板名已被占用": {
			"zh-TW": "模板名稱已被佔用", "en": "Template name already in use", "ja": "テンプレート名は既に使用されています",
			"ko": "템플릿 이름이 이미 사용 중입니다", "es": "El nombre de la plantilla ya está en uso",
			"fr": "Le nom du modèle est déjà utilisé", "de": "Vorlagenname wird bereits verwendet",
		},
		"流水线串联服务未初始化": {
			"zh-TW": "流水線串聯服務未初始化", "en": "Pipeline chaining service not initialized", "ja": "パイプライン連結サービスが初期化されていません",
			"ko": "파이프라인 연결 서비스가 초기화되지 않았습니다", "es": "El servicio de encadenamiento de pipelines no está inicializado",
			"fr": "Service de chaînage des pipelines non initialisé", "de": "Pipeline-Verkettungsdienst nicht initialisiert",
		},
		"源码读取所需服务未初始化": {
			"zh-TW": "讀取原始碼所需的服務未初始化", "en": "Service required for source code reading not initialized", "ja": "ソースコード読み取りに必要なサービスが初期化されていません",
			"ko": "소스 코드 읽기에 필요한 서비스가 초기화되지 않았습니다", "es": "El servicio necesario para leer el código fuente no está inicializado",
			"fr": "Le service requis pour la lecture du code source n'est pas initialisé", "de": "Der für das Lesen des Quellcodes erforderliche Dienst ist nicht initialisiert",
		},
		"环境名非法": {
			"zh-TW": "環境名稱非法", "en": "Invalid environment name", "ja": "環境名が不正です",
			"ko": "환경 이름이 잘못되었습니다", "es": "Nombre de entorno no válido",
			"fr": "Nom d'environnement invalide", "de": "Ungültiger Umgebungsname",
		},
		"登录用户不能为空": {
			"zh-TW": "登入使用者不能為空", "en": "Login user must not be empty", "ja": "ログインユーザーを空にすることはできません",
			"ko": "로그인 사용자는 비워 둘 수 없습니다", "es": "El usuario de inicio de sesión no puede estar vacío",
			"fr": "L'utilisateur de connexion ne doit pas être vide", "de": "Anmeldebenutzer darf nicht leer sein",
		},
		"缺少补全前缀": {
			"zh-TW": "缺少補全前綴", "en": "Missing completion prefix", "ja": "補完のプレフィックスがありません",
			"ko": "자동 완성 접두사가 없습니다", "es": "Falta el prefijo de autocompletado",
			"fr": "Préfixe de complétion manquant", "de": "Vervollständigungspräfix fehlt",
		},
		"自定义节点服务未初始化": {
			"zh-TW": "自訂節點服務未初始化", "en": "Custom node service not initialized", "ja": "カスタムノードサービスが初期化されていません",
			"ko": "사용자 지정 노드 서비스가 초기화되지 않았습니다", "es": "El servicio de nodos personalizados no está inicializado",
			"fr": "Service de nœuds personnalisés non initialisé", "de": "Dienst für benutzerdefinierte Knoten nicht initialisiert",
		},
		"认证服务未初始化": {
			"zh-TW": "認證服務未初始化", "en": "Authentication service not initialized", "ja": "認証サービスが初期化されていません",
			"ko": "인증 서비스가 초기화되지 않았습니다", "es": "El servicio de autenticación no está inicializado",
			"fr": "Service d'authentification non initialisé", "de": "Authentifizierungsdienst nicht initialisiert",
		},
		"该产物未归档真字节(占位/镜像类),无法下载": {
			"zh-TW": "該產物未歸檔真實位元組(佔位/鏡像類),無法下載", "en": "This artifact has no archived bytes (placeholder/image type); cannot download", "ja": "この成果物には実バイトがアーカイブされていないため(プレースホルダー/イメージ系)、ダウンロードできません",
			"ko": "이 산출물에는 실제 바이트가 보관되어 있지 않아(플레이스홀더/이미지 유형) 다운로드할 수 없습니다", "es": "Este artefacto no tiene bytes archivados (tipo marcador de posición/imagen); no se puede descargar",
			"fr": "Cet artefact n'a pas d'octets archivés (type espace réservé/image) ; téléchargement impossible", "de": "Dieses Artefakt hat keine archivierten Bytes (Platzhalter-/Image-Typ); Download nicht möglich",
		},
		"该运行尚未部署过,无可重试目标": {
			"zh-TW": "該執行尚未部署過,無可重試的目標", "en": "This run has never been deployed; no target to retry", "ja": "この実行はまだデプロイされていないため、再試行対象がありません",
			"ko": "이 실행은 아직 배포된 적이 없어 재시도할 대상이 없습니다", "es": "Esta ejecución nunca se ha desplegado; no hay destino para reintentar",
			"fr": "Cette exécution n'a jamais été déployée ; aucune cible à réessayer", "de": "Diese Ausführung wurde nie bereitgestellt; kein Ziel zum erneuten Versuch",
		},
		"该项目有进行中的运行,无法删除": {
			"zh-TW": "該專案有進行中的執行,無法刪除", "en": "This project has runs in progress; cannot delete", "ja": "このプロジェクトには進行中の実行があるため、削除できません",
			"ko": "이 프로젝트에 진행 중인 실행이 있어 삭제할 수 없습니다", "es": "Este proyecto tiene ejecuciones en curso; no se puede eliminar",
			"fr": "Ce projet a des exécutions en cours ; suppression impossible", "de": "Dieses Projekt hat laufende Ausführungen; Löschen nicht möglich",
		},
		"请求体格式错误": {
			"zh-TW": "請求體格式錯誤", "en": "Malformed request body", "ja": "リクエストボディの形式が不正です",
			"ko": "요청 본문 형식이 잘못되었습니다", "es": "Cuerpo de la solicitud con formato incorrecto",
			"fr": "Corps de requête mal formé", "de": "Fehlerhafter Anfragetext",
		},
		"路径不存在": {
			"zh-TW": "路徑不存在", "en": "Path not found", "ja": "パスが見つかりません",
			"ko": "경로를 찾을 수 없습니다", "es": "Ruta no encontrada",
			"fr": "Chemin introuvable", "de": "Pfad nicht gefunden",
		},
		"运行已结束,无法取消": {
			"zh-TW": "執行已結束,無法取消", "en": "Run has already finished; cannot cancel", "ja": "実行は既に終了しているため、キャンセルできません",
			"ko": "실행이 이미 종료되어 취소할 수 없습니다", "es": "La ejecución ya ha finalizado; no se puede cancelar",
			"fr": "L'exécution est déjà terminée ; annulation impossible", "de": "Die Ausführung ist bereits beendet; Abbrechen nicht möglich",
		},
		"通知渠道不存在": {
			"zh-TW": "通知通道不存在", "en": "Notification channel not found", "ja": "通知チャネルが見つかりません",
			"ko": "알림 채널을 찾을 수 없습니다", "es": "Canal de notificación no encontrado",
			"fr": "Canal de notification introuvable", "de": "Benachrichtigungskanal nicht gefunden",
		},
		"非法动作(start/stop/restart/down/update)": {
			"zh-TW": "非法動作(start/stop/restart/down/update)", "en": "Invalid action (start/stop/restart/down/update)", "ja": "不正なアクション(start/stop/restart/down/update)",
			"ko": "잘못된 작업(start/stop/restart/down/update)", "es": "Acción no válida (start/stop/restart/down/update)",
			"fr": "Action invalide (start/stop/restart/down/update)", "de": "Ungültige Aktion (start/stop/restart/down/update)",
		},
		"项目服务未初始化": {
			"zh-TW": "專案服務未初始化", "en": "Project service not initialized", "ja": "プロジェクトサービスが初期化されていません",
			"ko": "프로젝트 서비스가 초기화되지 않았습니다", "es": "El servicio de proyectos no está inicializado",
			"fr": "Service de projet non initialisé", "de": "Projektdienst nicht initialisiert",
		},
	})
}
