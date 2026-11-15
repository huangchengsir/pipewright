package authz

import "slices"

// CatalogVersion changes when an action's meaning or prerequisites change.
const CatalogVersion = 1

type Action struct {
	OwnerOnly           bool   `json:"ownerOnly"`
	ID                  string `json:"id"`
	Page                string `json:"page"`
	ResourceKind        string `json:"resourceKind"`
	ProjectScoped       bool   `json:"projectScoped"`
	EnvironmentRequired bool   `json:"environmentRequired"`
	Sensitive           bool   `json:"sensitive"`
	InstanceOnly        bool   `json:"instanceOnly"`
	ReadAction          string `json:"readAction,omitempty"`
}

// Entries are explicit. Unknown and future action identifiers are denied.
var actions = []Action{
	{ID: "opschat.read", Page: "page.opschat", ResourceKind: "ops_chat", OwnerOnly: true},
	{ID: "opschat.create", Page: "page.opschat", ResourceKind: "ops_chat", OwnerOnly: true, Sensitive: true},
	{ID: "opschat.edit", Page: "page.opschat", ResourceKind: "ops_chat", OwnerOnly: true, Sensitive: true, ReadAction: "opschat.read"},
	{ID: "opschat.delete", Page: "page.opschat", ResourceKind: "ops_chat", OwnerOnly: true, Sensitive: true, ReadAction: "opschat.read"},
	{ID: "opschat.submit", Page: "page.opschat", ResourceKind: "ops_chat", OwnerOnly: true, Sensitive: true, ReadAction: "opschat.read"},
	{ID: "opschat.confirm", Page: "page.opschat", ResourceKind: "ops_chat", OwnerOnly: true, Sensitive: true, ReadAction: "opschat.read"},
	{ID: "opschat.cancel", Page: "page.opschat", ResourceKind: "ops_chat", OwnerOnly: true, Sensitive: true, ReadAction: "opschat.read"},
	{ID: "mcp.approve", Page: "page.mcp", ResourceKind: "project", ProjectScoped: true, Sensitive: true, ReadAction: "pipeline.read"},
	{ID: "mcp.issue", Page: "page.mcp", ResourceKind: "project", ProjectScoped: true, Sensitive: true, ReadAction: "project.read"},
	{ID: "project.read", Page: "page.projects", ResourceKind: "project", ProjectScoped: true},
	{ID: "project.create", Page: "page.projects", ResourceKind: "project", Sensitive: true},
	{ID: "project.edit", Page: "page.projects", ResourceKind: "project", ProjectScoped: true, Sensitive: true, ReadAction: "project.read"},
	{ID: "project.delete", Page: "page.projects", ResourceKind: "project", ProjectScoped: true, Sensitive: true, ReadAction: "project.read"},
	{ID: "pipeline.read", Page: "page.pipeline", ResourceKind: "project", ProjectScoped: true},
	{ID: "pipeline.edit", Page: "page.pipeline", ResourceKind: "project", ProjectScoped: true, Sensitive: true, ReadAction: "pipeline.read"},
	{ID: "run.read", Page: "page.runs", ResourceKind: "run", ProjectScoped: true},
	{ID: "run.start", Page: "page.runs", ResourceKind: "run", ProjectScoped: true, ReadAction: "run.read"},
	{ID: "run.diagnose", Page: "page.runs", ResourceKind: "run", ProjectScoped: true, Sensitive: true, ReadAction: "run.read"},
	{ID: "run.approve", Page: "page.runs", ResourceKind: "run", ProjectScoped: true, Sensitive: true, ReadAction: "run.read"},
	{ID: "run.retry", Page: "page.runs", ResourceKind: "run", ProjectScoped: true, ReadAction: "run.read"},
	{ID: "run.cancel", Page: "page.runs", ResourceKind: "run", ProjectScoped: true, ReadAction: "run.read"},
	{ID: "artifact.download", Page: "page.runs", ResourceKind: "run", ProjectScoped: true, ReadAction: "run.read"},
	{ID: "deployment.execute", Page: "page.runs", ResourceKind: "environment", ProjectScoped: true, EnvironmentRequired: true, Sensitive: true, ReadAction: "environment.read"},
	{ID: "release.promote", Page: "page.runs", ResourceKind: "run", ProjectScoped: true, EnvironmentRequired: true, Sensitive: true, ReadAction: "run.read"},
	{ID: "release.approve", Page: "page.runs", ResourceKind: "run", ProjectScoped: true, EnvironmentRequired: true, Sensitive: true, ReadAction: "run.read"},
	{ID: "release.rollback", Page: "page.environments", ResourceKind: "environment", ProjectScoped: true, EnvironmentRequired: true, Sensitive: true, ReadAction: "environment.read"},
	{ID: "environment.read", Page: "page.environments", ResourceKind: "environment", ProjectScoped: true},
	{ID: "guard.read", Page: "page.guard", ResourceKind: "run", ProjectScoped: true},
	{ID: "guard.observe", Page: "page.guard", ResourceKind: "run", ProjectScoped: true, Sensitive: true, ReadAction: "guard.read"},
	{ID: "guard.stop", Page: "page.guard", ResourceKind: "run", ProjectScoped: true, Sensitive: true, ReadAction: "guard.read"},
	{ID: "guard.delete", Page: "page.guard", ResourceKind: "run", ProjectScoped: true, Sensitive: true, ReadAction: "guard.read"},
	{ID: "guard.investigate", Page: "page.guard", ResourceKind: "run", ProjectScoped: true, Sensitive: true, ReadAction: "guard.read"},
	{ID: "guard.analyze", Page: "page.guard", ResourceKind: "run", ProjectScoped: true, Sensitive: true, ReadAction: "guard.read"},
	{ID: "server.assign", Page: "page.servers", ResourceKind: "server", Sensitive: true, InstanceOnly: true},
	{ID: "server.inspect", Page: "page.servers", ResourceKind: "server"},
	{ID: "server.create", Page: "page.servers", ResourceKind: "server", Sensitive: true},
	{ID: "server.edit", Page: "page.servers", ResourceKind: "server", Sensitive: true, ReadAction: "server.inspect"},
	{ID: "server.delete", Page: "page.servers", ResourceKind: "server", Sensitive: true, ReadAction: "server.inspect"},
	{ID: "server.use", Page: "page.servers", ResourceKind: "server", Sensitive: true, ReadAction: "server.inspect"},
	{ID: "server.terminal", Page: "page.servers", ResourceKind: "server", Sensitive: true, ReadAction: "server.inspect"},
	{ID: "container.inspect", Page: "page.containers", ResourceKind: "server"},
	{ID: "container.operate", Page: "page.containers", ResourceKind: "server", Sensitive: true, ReadAction: "container.inspect"},
	{ID: "container.terminal", Page: "page.containers", ResourceKind: "server", Sensitive: true, ReadAction: "container.inspect"},
	{ID: "container.reveal", Page: "page.containers", ResourceKind: "server", Sensitive: true, ReadAction: "container.inspect"},
	{ID: "container.diagnose", Page: "page.containers", ResourceKind: "server", Sensitive: true, ReadAction: "container.inspect"},
	{ID: "kubernetes.read", Page: "page.kubernetes", ResourceKind: "kubernetes"},
	{ID: "kubernetes.create", Page: "page.kubernetes", ResourceKind: "kubernetes", Sensitive: true},
	{ID: "kubernetes.edit", Page: "page.kubernetes", ResourceKind: "kubernetes", Sensitive: true, ReadAction: "kubernetes.read"},
	{ID: "kubernetes.delete", Page: "page.kubernetes", ResourceKind: "kubernetes", Sensitive: true, ReadAction: "kubernetes.read"},
	{ID: "kubernetes_credential.create", Page: "page.kubernetes", ResourceKind: "kubernetes_credential", Sensitive: true},
	{ID: "kubernetes_credential.edit", Page: "page.kubernetes", ResourceKind: "kubernetes_credential", Sensitive: true, ReadAction: "kubernetes_credential.read"},
	{ID: "kubernetes_credential.delete", Page: "page.kubernetes", ResourceKind: "kubernetes_credential", Sensitive: true, ReadAction: "kubernetes_credential.read"},
	{ID: "kubernetes.use", Page: "page.kubernetes", ResourceKind: "kubernetes", Sensitive: true, ReadAction: "kubernetes.read"},
	{ID: "kubernetes_credential.read", Page: "page.kubernetes", ResourceKind: "kubernetes_credential"},
	{ID: "kubernetes_credential.use", Page: "page.kubernetes", ResourceKind: "kubernetes_credential", Sensitive: true, ReadAction: "kubernetes_credential.read"},
	{ID: "kubernetes.deploy", Page: "page.kubernetes", ResourceKind: "kubernetes", EnvironmentRequired: true, Sensitive: true, ReadAction: "kubernetes.read"},
	{ID: "credential.metadata", Page: "page.vault", ResourceKind: "credential"},
	{ID: "credential.create", Page: "page.vault", ResourceKind: "credential", Sensitive: true},
	{ID: "credential.edit", Page: "page.vault", ResourceKind: "credential", Sensitive: true, ReadAction: "credential.metadata"},
	{ID: "credential.delete", Page: "page.vault", ResourceKind: "credential", Sensitive: true, ReadAction: "credential.metadata"},
	{ID: "credential.use", Page: "page.vault", ResourceKind: "credential", Sensitive: true, ReadAction: "credential.metadata"},
	{ID: "credential.reveal", Page: "page.vault", ResourceKind: "credential", Sensitive: true, ReadAction: "credential.metadata"},
	{ID: "member.read", Page: "page.members", ResourceKind: "member"},
	{ID: "member.manage", Page: "page.members", ResourceKind: "member", Sensitive: true, ReadAction: "member.read"},
	{ID: "role.read", Page: "page.roles", ResourceKind: "role"},
	{ID: "role.manage", Page: "page.roles", ResourceKind: "role", Sensitive: true, ReadAction: "role.read"},
	{ID: "template.read", Page: "page.library", ResourceKind: "template"},
	{ID: "template.create", Page: "page.library", ResourceKind: "template", Sensitive: true},
	{ID: "template.edit", Page: "page.library", ResourceKind: "template", Sensitive: true, ReadAction: "template.read"},
	{ID: "template.delete", Page: "page.library", ResourceKind: "template", Sensitive: true, ReadAction: "template.read"},
	{ID: "variable_group.read", Page: "page.library", ResourceKind: "variable_group"},
	{ID: "variable_group.create", Page: "page.library", ResourceKind: "variable_group", Sensitive: true},
	{ID: "variable_group.edit", Page: "page.library", ResourceKind: "variable_group", Sensitive: true, ReadAction: "variable_group.read"},
	{ID: "variable_group.delete", Page: "page.library", ResourceKind: "variable_group", Sensitive: true, ReadAction: "variable_group.read"},
	{ID: "custom_node.read", Page: "page.library", ResourceKind: "custom_node"},
	{ID: "custom_node.create", Page: "page.library", ResourceKind: "custom_node", Sensitive: true},
	{ID: "custom_node.edit", Page: "page.library", ResourceKind: "custom_node", Sensitive: true, ReadAction: "custom_node.read"},
	{ID: "custom_node.delete", Page: "page.library", ResourceKind: "custom_node", Sensitive: true, ReadAction: "custom_node.read"},
	{ID: "audit.read", Page: "page.audit", ResourceKind: "audit", ProjectScoped: true},
	{ID: "preview.provision", Page: "page.previews", ResourceKind: "project", ProjectScoped: true, Sensitive: true, ReadAction: "project.read"},
	{ID: "preview.reclaim", Page: "page.previews", ResourceKind: "preview", ProjectScoped: true, Sensitive: true, ReadAction: "preview.read"},
	{ID: "preview.read", Page: "page.previews", ResourceKind: "preview", ProjectScoped: true},
	{ID: "proxy.read", Page: "page.containers", ResourceKind: "server", ReadAction: "server.inspect"},
	{ID: "proxy.manage", Page: "page.containers", ResourceKind: "server", Sensitive: true, ReadAction: "proxy.read"},
	{ID: "proxy.install", Page: "page.containers", ResourceKind: "server", Sensitive: true, ReadAction: "proxy.read"},
	{ID: "dns.use", Page: "page.containers", ResourceKind: "dns_provider", Sensitive: true},
	{ID: "certificate.read", Page: "page.certificates", ResourceKind: "proxy_route"},
	{ID: "certificate.refresh", Page: "page.certificates", ResourceKind: "proxy_route", Sensitive: true, ReadAction: "certificate.read"},
	{ID: "anomaly.read", Page: "page.anomalies", ResourceKind: "server", ReadAction: "server.inspect"},
	{ID: "anomaly.manage", Page: "page.anomalies", ResourceKind: "server", Sensitive: true, ReadAction: "anomaly.read"},
	{ID: "anomaly.analyze", Page: "page.anomalies", ResourceKind: "server", Sensitive: true, ReadAction: "anomaly.read"},
	{ID: "notification.read", Page: "page.runs", ResourceKind: "notification_channel"},
	{ID: "notification.send", Page: "page.runs", ResourceKind: "notification_channel", Sensitive: true, ReadAction: "notification.read"},
	{ID: "dashboard.read", Page: "page.dashboard", ResourceKind: "project", ProjectScoped: true},
	{ID: "dora.read", Page: "page.dora", ResourceKind: "project", ProjectScoped: true},
	{ID: "system.manage", Page: "page.system", ResourceKind: "instance", Sensitive: true, InstanceOnly: true},
	{ID: "database.migrate", Page: "page.database", ResourceKind: "instance", Sensitive: true, InstanceOnly: true},
}

func Catalog() []Action { return slices.Clone(actions) }
func Lookup(id string) (Action, bool) {
	for _, a := range actions {
		if a.ID == id {
			return a, true
		}
	}
	return Action{}, false
}
