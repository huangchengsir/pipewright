package i18n

func init() {
	register(map[string]map[string]string{
		"Kubernetes 发布配置无效，请检查来源和资源设置": {
			"zh-TW": "Kubernetes 發布設定無效，請檢查來源和資源設定",
			"en":    "Invalid Kubernetes release configuration. Check the source and resource settings.",
			"ja":    "Kubernetes リリース設定が無効です。ソースとリソースの設定を確認してください。",
			"ko":    "Kubernetes 릴리스 구성이 올바르지 않습니다. 소스와 리소스 설정을 확인하세요.",
			"es":    "La configuración de publicación de Kubernetes no es válida. Comprueba el origen y los recursos.",
			"fr":    "La configuration de déploiement Kubernetes est invalide. Vérifiez la source et les ressources.",
			"de":    "Ungültige Kubernetes-Release-Konfiguration. Prüfen Sie die Quell- und Ressourceneinstellungen.",
		},
	})
}
