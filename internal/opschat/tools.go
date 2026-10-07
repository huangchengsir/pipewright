package opschat

import (
	"encoding/json"
	"github.com/huangchengsir/pipewright/internal/target"
	"regexp"
	"strconv"
)

type Tool struct {
	ID       string          `json:"toolId"`
	Mutation bool            `json:"mutation"`
	Schema   json.RawMessage `json:"schema"`
}

var tools = []Tool{
	{"host_resources", false, json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)},
	{"host_processes", false, json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)},
	{"host_ports", false, json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)},
	{"docker_containers", false, json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)},
	{"docker_stats", false, json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)},
	{"docker_images", false, json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)},
	{"docker_inspect", false, json.RawMessage(`{"type":"object","properties":{"container":{"type":"string","maxLength":256}},"required":["container"],"additionalProperties":false}`)},
	{"docker_logs", false, json.RawMessage(`{"type":"object","properties":{"container":{"type":"string","maxLength":256},"lines":{"type":"integer","minimum":1,"maximum":1000,"default":200}},"required":["container"],"additionalProperties":false}`)},
	{"systemd_status", false, json.RawMessage(`{"type":"object","properties":{"unit":{"type":"string","maxLength":256}},"required":["unit"],"additionalProperties":false}`)},
	{"systemd_logs", false, json.RawMessage(`{"type":"object","properties":{"unit":{"type":"string","maxLength":256},"lines":{"type":"integer","minimum":1,"maximum":1000,"default":200}},"required":["unit"],"additionalProperties":false}`)},
	{"docker_action", true, json.RawMessage(`{"type":"object","properties":{"container":{"type":"string","maxLength":256},"action":{"type":"string","enum":["start","stop","restart","pause","unpause"]}},"required":["container","action"],"additionalProperties":false}`)},
	{"systemd_action", true, json.RawMessage(`{"type":"object","properties":{"unit":{"type":"string","maxLength":256},"action":{"type":"string","enum":["start","stop","restart"]}},"required":["unit","action"],"additionalProperties":false}`)},
}

func Tools() []Tool {
	out := make([]Tool, len(tools))
	copy(out, tools)
	for i := range out {
		out[i].Schema = append(json.RawMessage(nil), out[i].Schema...)
	}
	return out
}
func mutation(id string) bool { return id == "docker_action" || id == "systemd_action" }
func parseArgs(id string, raw json.RawMessage) (ToolArgs, error) {
	var a ToolArgs
	if err := strict(raw, &a); err != nil {
		return a, err
	}
	var supplied map[string]json.RawMessage
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &supplied)
	}
	allowed := map[string]bool{}
	found := false
	for _, t := range tools {
		if t.ID == id {
			found = true
		}
	}
	if !found {
		return a, ErrInvalid
	}
	switch id {
	case "docker_inspect", "docker_logs", "docker_action":
		allowed["container"] = true
		if target.ValidateServiceParams("docker", a.Container, "start") != nil {
			return a, ErrInvalid
		}
	case "systemd_status", "systemd_logs", "systemd_action":
		allowed["unit"] = true
		if target.ValidateServiceParams("systemd", a.Unit, "start") != nil {
			return a, ErrInvalid
		}
	}
	if id == "docker_logs" || id == "systemd_logs" {
		allowed["lines"] = true
		if a.Lines == 0 {
			if _, ok := supplied["lines"]; ok {
				return a, ErrInvalid
			}
			a.Lines = 200
		}
		if a.Lines < 1 || a.Lines > 1000 {
			return a, ErrInvalid
		}
	}
	if mutation(id) {
		allowed["action"] = true
		typ, obj := "docker", a.Container
		if id == "systemd_action" {
			typ, obj = "systemd", a.Unit
		}
		if target.ValidateOpsAction(typ, obj, a.Action) != nil {
			return a, ErrInvalid
		}
	}
	for k := range supplied {
		if !allowed[k] {
			return a, ErrInvalid
		}
	}
	return a, nil
}

var immutableContainer = regexp.MustCompile(`^[a-f0-9]{64}$`)

const inspectProjection = `{"id":{{json .Id}},"state":{{json .State}},"image":{{json .Image}},"ports":{{json .NetworkSettings.Ports}},"mounts":{{json .Mounts}}}`

func commands(id string, a ToolArgs) [][]string {
	switch id {
	case "host_resources":
		return [][]string{{"env", "LC_ALL=C", "df", "-P", "-k"}, {"env", "LC_ALL=C", "free", "-b"}, {"env", "LC_ALL=C", "uptime"}}
	case "host_processes":
		return [][]string{{"ps", "-eo", "pid,comm,pcpu,pmem", "--sort=-pcpu"}}
	case "host_ports":
		return [][]string{{"ss", "-lntu"}}
	case "docker_containers":
		return [][]string{{"docker", "ps", "-a", "--no-trunc", "--format", `{"id":{{json .ID}},"names":{{json .Names}},"image":{{json .Image}},"state":{{json .State}},"ports":{{json .Ports}}}`}}
	case "docker_stats":
		return [][]string{{"docker", "stats", "--no-stream", "--format", `{"id":{{json .ID}},"name":{{json .Name}},"cpu":{{json .CPUPerc}},"memory":{{json .MemUsage}},"network":{{json .NetIO}},"block":{{json .BlockIO}}}`}}
	case "docker_images":
		return [][]string{{"docker", "images", "--no-trunc", "--format", `{"id":{{json .ID}},"repository":{{json .Repository}},"tag":{{json .Tag}},"size":{{json .Size}}}`}}
	case "docker_inspect":
		return [][]string{{"docker", "inspect", "--type", "container", "--format", inspectProjection, a.Container}}
	case "docker_logs":
		return [][]string{{"docker", "logs", "--tail", strconv.Itoa(a.Lines), "--timestamps", a.Container}}
	case "systemd_status":
		return [][]string{{"systemctl", "show", a.Unit, "--property=Id,ActiveState,SubState,LoadState,MainPID"}}
	case "systemd_logs":
		return [][]string{{"journalctl", "-u", a.Unit, "-n", strconv.Itoa(a.Lines), "--no-pager", "-o", "short-iso"}}
	case "docker_action":
		return [][]string{target.BuildServiceCmd("docker", a.Action, a.Container)}
	case "systemd_action":
		return [][]string{target.BuildServiceCmd("systemd", a.Action, a.Unit)}
	}
	return nil
}
