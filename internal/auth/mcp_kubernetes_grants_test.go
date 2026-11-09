package auth

import (
	"encoding/json"
	"testing"
)

func TestMCPKubernetesConfigurationRequiresNewSectionOperationAndSelectors(t *testing.T) {
	old := MCPConfigGrant{ProjectID: "project", Read: true, Propose: true, Apply: true, Sections: []string{"pipeline"}, Operations: []string{"job.rename"}}
	normalized, e := NormalizeMCPConfigGrants([]string{"project"}, []MCPConfigGrant{old})
	if e != nil {
		t.Fatal(e)
	}
	token := &MCPToken{ProjectIDs: []string{"project"}, ConfigGrants: normalized}
	if token.AllowsConfigNode("project", "propose", "kubernetes.add", "deploy", "new") || token.AllowsConfigNode("project", "propose", "kubernetes.update", "deploy", "existing") {
		t.Fatal("legacy grants expanded")
	}
	body, _ := json.Marshal(normalized[0])
	if string(body) == "" || len(normalized[0].StageIDs) > 0 || len(normalized[0].JobIDs) > 0 {
		t.Fatal("selectors inferred")
	}
	fresh := MCPConfigGrant{ProjectID: "project", Read: true, Propose: true, Apply: true, Sections: []string{"kubernetes"}, Operations: []string{"kubernetes.add", "kubernetes.update"}, StageIDs: []string{"deploy"}, JobIDs: []string{"existing"}}
	normalized, e = NormalizeMCPConfigGrants([]string{"project"}, []MCPConfigGrant{fresh})
	if e != nil {
		t.Fatal(e)
	}
	token.ConfigGrants = normalized
	if !token.AllowsConfigNode("project", "propose", "kubernetes.add", "deploy", "new") || !token.AllowsConfigNode("project", "apply", "kubernetes.update", "deploy", "existing") {
		t.Fatal("explicit grant unavailable")
	}
	for _, input := range [][4]string{{"project", "kubernetes.add", "other", "new"}, {"other", "kubernetes.add", "deploy", "new"}, {"project", "kubernetes.update", "deploy", "ungranted"}} {
		if token.AllowsConfigNode(input[0], "propose", input[1], input[2], input[3]) {
			t.Fatal("configuration escaped exact selectors", input)
		}
	}
	fresh.StageIDs = nil
	if _, e = NormalizeMCPConfigGrants([]string{"project"}, []MCPConfigGrant{fresh}); e == nil {
		t.Fatal("new K8s operations without stage scope accepted")
	}
	fresh.StageIDs = []string{"deploy"}
	fresh.JobIDs = nil
	if _, e = NormalizeMCPConfigGrants([]string{"project"}, []MCPConfigGrant{fresh}); e == nil {
		t.Fatal("update without exact jobs accepted")
	}
}
