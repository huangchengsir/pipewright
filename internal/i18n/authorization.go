package i18n

func init() {
	register(map[string]map[string]string{
		"没有此操作的权限":       {"zh-TW": "沒有此操作的權限", "en": "You do not have permission for this action.", "ja": "この操作を行う権限がありません。", "ko": "이 작업을 수행할 권한이 없습니다.", "es": "No tienes permiso para esta acción.", "fr": "Vous n’avez pas l’autorisation d’effectuer cette action.", "de": "Sie haben keine Berechtigung für diese Aktion."},
		"权限状态已变化，请刷新后重试": {"zh-TW": "權限狀態已變更，請重新整理後再試", "en": "Permissions have changed. Refresh and try again.", "ja": "権限が変更されました。更新してから再試行してください。", "ko": "권한이 변경되었습니다. 새로 고친 후 다시 시도하세요.", "es": "Los permisos han cambiado. Actualiza la página e inténtalo de nuevo.", "fr": "Les autorisations ont changé. Actualisez la page et réessayez.", "de": "Die Berechtigungen haben sich geändert. Aktualisieren Sie die Seite und versuchen Sie es erneut."},
		"维护期间暂不能修改配置":    {"zh-TW": "維護期間暫時無法修改設定", "en": "Configuration changes are unavailable during maintenance.", "ja": "メンテナンス中は設定を変更できません。", "ko": "유지 관리 중에는 구성을 변경할 수 없습니다.", "es": "No se puede modificar la configuración durante el mantenimiento.", "fr": "La configuration ne peut pas être modifiée pendant la maintenance.", "de": "Während der Wartung kann die Konfiguration nicht geändert werden."},
	})
}
