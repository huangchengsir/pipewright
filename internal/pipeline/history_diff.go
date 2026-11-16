package pipeline

import (
	"encoding/json"
	"math"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

type HistoryChange struct {
	Kind    string               `json:"kind"`
	ID      string               `json:"id"`
	Name    string               `json:"name"`
	Action  string               `json:"action"`
	Fields  []string             `json:"fields"`
	Details []HistoryFieldChange `json:"details,omitempty"`
}

type HistoryFieldChange struct {
	Field  string `json:"field"`
	Before string `json:"before,omitempty"`
	After  string `json:"after,omitempty"`
}

type historyNode struct {
	id, kind, name, parentID, parentName string
	position                             int
	stage                                *Stage
	job                                  *Job
}

func historyNodes(spec Spec) ([]historyNode, map[string]historyNode) {
	nodes := make([]historyNode, 0, len(spec.Stages))
	for i := range spec.Stages {
		stage := &spec.Stages[i]
		nodes = append(nodes, historyNode{id: stage.ID, kind: "stage", name: stage.Name, position: i, stage: stage})
		for j := range stage.Jobs {
			job := &stage.Jobs[j]
			nodes = append(nodes, historyNode{id: job.ID, kind: "job", name: job.Name, parentID: stage.ID, parentName: stage.Name, position: j, job: job})
		}
	}
	index := make(map[string]historyNode, len(nodes))
	for _, node := range nodes {
		index[historyNodeKey(node)] = node
	}
	return nodes, index
}

func historyNodeKey(node historyNode) string { return node.kind + ":" + node.id }

func diffHistory(before, after Spec) []HistoryChange {
	oldNodes, oldIndex := historyNodes(before)
	newNodes, newIndex := historyNodes(after)
	oldOrder := historySurvivingOrder(oldNodes, newIndex)
	newOrder := historySurvivingOrder(newNodes, oldIndex)
	changes := []HistoryChange{}
	for _, node := range newNodes {
		key := historyNodeKey(node)
		if previous, ok := oldIndex[key]; ok {
			change := compareHistoryNodes(previous, node, oldOrder[key] != newOrder[key])
			if len(change.Fields) != 0 {
				changes = append(changes, change)
			}
		} else {
			changes = append(changes, HistoryChange{Kind: node.kind, ID: node.id, Name: node.name, Action: "added", Fields: []string{}})
		}
	}
	for _, node := range oldNodes {
		if _, exists := newIndex[historyNodeKey(node)]; !exists {
			changes = append(changes, HistoryChange{Kind: node.kind, ID: node.id, Name: node.name, Action: "removed", Fields: []string{}})
		}
	}
	return changes
}

// Insertions or removals alone do not move surviving siblings.
func historySurvivingOrder(nodes []historyNode, other map[string]historyNode) map[string]int {
	result := make(map[string]int, len(nodes))
	positions := map[string]int{}
	for _, node := range nodes {
		key := historyNodeKey(node)
		if previous, ok := other[key]; ok && previous.parentID == node.parentID {
			result[key] = positions[node.parentID]
			positions[node.parentID]++
		}
	}
	return result
}

func compareHistoryNodes(before, after historyNode, reordered bool) HistoryChange {
	change := HistoryChange{Kind: after.kind, ID: after.id, Name: after.name, Action: "changed", Fields: []string{}}
	mark := func(name string) { change.Fields = append(change.Fields, name) }
	safe := func(name, left, right string) {
		if left != right {
			mark(name)
			change.Details = append(change.Details, HistoryFieldChange{Field: name, Before: left, After: right})
		}
	}
	needs := func(left, right []string) {
		if !slices.Equal(left, right) {
			safe("needs", formatHistoryNeeds(left), formatHistoryNeeds(right))
		}
	}
	safe("name", before.name, after.name)
	if before.parentID != after.parentID {
		mark("stage")
		change.Details = append(change.Details, HistoryFieldChange{Field: "stage", Before: before.parentName, After: after.parentName})
	}
	if reordered {
		mark("position")
		change.Details = append(change.Details, HistoryFieldChange{Field: "position", Before: strconv.Itoa(before.position + 1), After: strconv.Itoa(after.position + 1)})
	}
	if before.stage != nil {
		a, b := before.stage, after.stage
		safe("kind", a.Kind, b.Kind)
		needs(a.Needs, b.Needs)
		safe("gate", strconv.FormatBool(a.Gate), strconv.FormatBool(b.Gate))
		safe("allowFailure", strconv.FormatBool(a.AllowFailure), strconv.FormatBool(b.AllowFailure))
		if !reflect.DeepEqual(a.When, b.When) {
			mark("when")
		}
		if !reflect.DeepEqual(a.Matrix, b.Matrix) {
			mark("matrix")
		}
		if !reflect.DeepEqual(a.Post, b.Post) {
			mark("post")
		}
		if !reflect.DeepEqual(a.Services, b.Services) {
			mark("services")
		}
	} else {
		a, b := before.job, after.job
		safe("type", a.Type, b.Type)
		needs(a.Needs, b.Needs)
		if a.Summary != b.Summary {
			mark("summary")
		}
		change.Fields = append(change.Fields, changedHistoryConfigFields(a.Config, b.Config)...)

	}
	switch {
	case before.parentID != after.parentID || reordered:
		change.Action = "moved"
	case len(change.Fields) == 1 && change.Fields[0] == "name":
		change.Action = "renamed"
	}
	return change
}

func formatHistoryNeeds(ids []string) string {
	if len(ids) == 0 {
		return ""
	}
	encoded, _ := json.Marshal(ids)
	return string(encoded)
}

func changedHistoryConfigFields(before, after map[string]any) []string {
	changed := map[string]bool{}
	for key, old := range before {
		if current, ok := after[key]; !ok || !reflect.DeepEqual(old, current) {
			changed[historyConfigField(key)] = true
		}
	}
	for key := range after {
		if _, existed := before[key]; !existed {
			changed[historyConfigField(key)] = true
		}
	}
	var fields []string
	for _, field := range []string{"commands", "variables", "references", "protected", "config"} {
		if changed[field] {
			fields = append(fields, field)
		}
	}
	return fields
}

func historyConfigField(key string) string {
	switch key {
	case "command", "commands", "script", "deployCommand", "deployScript", "scriptEntrypoint":
		return "commands"
	case "env", "envs", "variables", "vars", "params", "buildArgs", "templateVariables", "variableBindings":
		return "variables"
	case "credentialId", "registryCredentialId", "gitCredentialId", "serverId", "runnerServerId", "connectionId", "targetServerIds", "serverIds":
		return "references"
	case "privateRef", "privateConfigRef":
		return "protected"
	default:
		return "config"
	}
}

// Drafts may reuse values already visible through the ordinary current editor.
// Older changed values are restricted to structurally safe bindings and a
// small set of common literal-free commands. This prevents resurrecting a
// replaced private payload, inline credential, variable, or arbitrary script.
var safeHistoricalCommand = regexp.MustCompile(`^(npm (ci|test|run (build|test|lint))|pnpm (install|test|run (build|test|lint))|go (test|build)( (-race|-v))?( \./\.\.\.)?|yarn (install|test|build|lint))$`)

func safeHistoricalDraft(history, current Spec) bool {
	historicalNodes, _ := historyNodes(history)
	_, currentIndex := historyNodes(current)
	for _, node := range historicalNodes {
		prior := currentIndex[historyNodeKey(node)]
		if node.stage != nil {
			old, now := node.stage, prior.stage
			if now == nil {
				now = &Stage{}
			}
			if !safeHistoricalWhen(old.When, now.When) || !safeHistoricalMatrix(old.Matrix, now.Matrix) ||
				!reflect.DeepEqual(old.Post, now.Post) || !reflect.DeepEqual(old.Services, now.Services) {
				return false
			}
			continue
		}
		var config map[string]any
		if prior.job != nil && prior.job.Type == node.job.Type {
			config = prior.job.Config
		}
		if summary := node.job.Summary; summary != "" &&
			(prior.job == nil || prior.job.Summary != summary) && !safeHistoricalCommands(summary) {
			return false
		}
		if !safeHistoricalJob(node.job.Config, config) {
			return false
		}
	}
	return true
}

func safeHistoricalWhen(history, current When) bool {
	if len(history.Branches) != 0 && !slices.Equal(history.Branches, current.Branches) {
		return false
	}
	for _, event := range history.Events {
		switch event {
		case "manual", "webhook", "schedule", "chain":
		default:
			if !slices.Equal(history.Events, current.Events) {
				return false
			}
		}
	}
	return true
}

// Matrix values are injected into container environments. A draft can reduce
// or reorder existing values, but cannot return an older value now removed.
func safeHistoricalMatrix(history, current map[string][]string) bool {
	for name, values := range history {
		existing, ok := current[name]
		if !ok {
			return false
		}
		for _, value := range values {
			if !slices.Contains(existing, value) {
				return false
			}
		}
	}
	return true
}

func safeHistoricalJob(history, current map[string]any) bool {
	if !reflect.DeepEqual(history["privateRef"], current["privateRef"]) ||
		!reflect.DeepEqual(history["privateConfigRef"], current["privateConfigRef"]) {
		return false
	}
	for key, value := range history {
		if reflect.DeepEqual(value, current[key]) {
			continue
		}
		switch key {
		case "credentialId", "registryCredentialId", "gitCredentialId", "serverId", "runnerServerId", "connectionId":
			id, ok := value.(string)
			if !ok {
				return false
			}
			if id != "" {
				parsed, err := uuid.Parse(id)
				if err != nil || parsed.String() != id {
					return false
				}
			}
		case "commands":
			if !safeHistoricalCommands(value) {
				return false
			}
		case "timeoutSeconds", "retries", "depth", "commandTimeoutSeconds", "connectTimeoutSeconds",
			"uploadIdleTimeoutSeconds", "uploadTimeoutSeconds", "expectStatus", "intervalSeconds", "observeSeconds":
			number, ok := value.(float64)
			if !ok || number < 0 || number > math.MaxInt32 || number != math.Trunc(number) {
				return false
			}
		case "localDocker":
			switch value {
			case true, false, "", "true", "false":
			default:
				return false
			}
		case "artifactType":
			switch value {
			case "", "image", "jar", "dist", "archive":
			default:
				return false
			}
		case "buildModel":
			switch value {
			case "", "dockerfile", "toolchain":
			default:
				return false
			}
		default:
			return false
		}
	}
	return true
}

func safeHistoricalCommands(value any) bool {
	switch commands := value.(type) {
	case string:
		for _, line := range strings.Split(commands, "\n") {
			if line = strings.TrimSpace(line); line != "" && !safeHistoricalCommand.MatchString(line) {
				return false
			}
		}
	case []any:
		for _, command := range commands {
			line, ok := command.(string)
			if !ok || !safeHistoricalCommand.MatchString(strings.TrimSpace(line)) {
				return false
			}
		}
	default:
		return false
	}
	return true
}
