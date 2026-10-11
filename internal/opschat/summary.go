package opschat

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
)

func (s *Service) summaryTx(ctx context.Context, tx *sql.Tx, r *runRecord) error {
	if r.Body.SummarySaved {
		return nil
	}
	calls, err := s.calls(ctx, tx, r.View.SessionID, r.View.ID)
	if err != nil {
		return err
	}
	if len(calls) > 0 {
		if err = s.append(ctx, tx, Entry{SessionID: r.View.SessionID, RunID: r.View.ID, Kind: "summary", Summary: s.summarize(calls)}, true, false); err != nil {
			return err
		}
	}
	r.Body.SummarySaved = true
	return nil
}

// Local summaries are bounded facts, never a diagnosis or future planning input.
type RunSummary struct {
	Servers []ServerSummary `json:"servers"`
}
type ServerSummary struct {
	ServerID   string     `json:"serverId"`
	ServerName string     `json:"serverName"`
	Facts      []CallFact `json:"facts"`
	Missing    []string   `json:"missing"`
}
type CallFact struct {
	Occupants []ResourceOccupant `json:"occupants,omitempty"`
	CallID    string             `json:"callId"`
	ToolID    string             `json:"toolId"`
	Status    string             `json:"status"`
	Truncated bool               `json:"truncated"`
	Error     string             `json:"error,omitempty"`
	Excerpt   string             `json:"excerpt,omitempty"`
	Resources *HostResources     `json:"resources,omitempty"`
}
type ResourceOccupant struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	CPUPercent    float64  `json:"cpuPercent"`
	MemoryPercent *float64 `json:"memoryPercent,omitempty"`
	MemoryUsage   string   `json:"memoryUsage,omitempty"`
	memoryBytes   float64
}

func metricPercent(value string, max float64) (float64, bool) {
	n, err := strconv.ParseFloat(strings.TrimSuffix(value, "%"), 64)
	return n, err == nil && !math.IsNaN(n) && !math.IsInf(n, 0) && n >= 0 && n <= max
}
func memoryBytes(value string) float64 {
	value = strings.TrimSpace(strings.SplitN(value, "/", 2)[0])
	for _, u := range []struct {
		name   string
		factor float64
	}{{"GiB", 1 << 30}, {"MiB", 1 << 20}, {"KiB", 1 << 10}, {"GB", 1e9}, {"MB", 1e6}, {"kB", 1e3}, {"B", 1}} {
		if strings.HasSuffix(value, u.name) {
			n, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimSuffix(value, u.name)), 64)
			if err == nil && n >= 0 && n <= 1e18 {
				return n * u.factor
			}
			return 0
		}
	}
	return 0
}
func (s *Service) occupants(v Call) ([]ResourceOccupant, bool) {
	all := []ResourceOccupant{}
	if v.Status != Succeeded && v.Status != PartialFailed {
		return nil, false
	}
	switch v.ToolID {
	case "host_processes":
		lines := strings.Split(strings.TrimSpace(v.Output), "\n")
		if len(lines) == 0 || strings.Join(strings.Fields(lines[0]), " ") != "PID COMMAND %CPU %MEM" {
			return nil, false
		}
		for _, line := range lines[1:] {
			f := strings.Fields(line)
			if len(f) != 4 {
				return nil, false
			}
			pid, err := strconv.ParseUint(f[0], 10, 64)
			cpu, ok := metricPercent(f[2], 100000)
			mem, mok := metricPercent(f[3], 100)
			if err != nil || pid == 0 || !ok || !mok {
				return nil, false
			}
			all = append(all, ResourceOccupant{ID: f[0], Name: s.scrub(f[1], 128), CPUPercent: cpu, MemoryPercent: &mem, memoryBytes: mem})
		}
	case "docker_stats":
		d := json.NewDecoder(strings.NewReader(v.Output))
		for {
			var row struct{ ID, Name, CPU, Memory string }
			err := d.Decode(&row)
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, false
			}
			cpu, ok := metricPercent(row.CPU, 100000)
			if !ok || row.ID == "" || row.Name == "" || len(all) >= 1000 {
				return nil, false
			}
			all = append(all, ResourceOccupant{ID: s.scrub(row.ID, 128), Name: s.scrub(row.Name, 128), CPUPercent: cpu, MemoryUsage: s.scrub(row.Memory, 128), memoryBytes: memoryBytes(row.Memory)})
		}
	default:
		return nil, false
	}
	// Preserve both CPU and memory occupiers, not only the first raw output lines.
	cpu := append([]ResourceOccupant(nil), all...)
	mem := append([]ResourceOccupant(nil), all...)
	sort.SliceStable(cpu, func(i, j int) bool { return cpu[i].CPUPercent > cpu[j].CPUPercent })
	sort.SliceStable(mem, func(i, j int) bool { return mem[i].memoryBytes > mem[j].memoryBytes })
	out := []ResourceOccupant{}
	seen := map[string]bool{}
	for _, list := range [][]ResourceOccupant{cpu, mem} {
		for _, o := range list[:min(3, len(list))] {
			if !seen[o.ID] {
				out = append(out, o)
				seen[o.ID] = true
			}
		}
	}
	return out, true
}

func (s *Service) summarize(calls []callRecord) *RunSummary {
	out := &RunSummary{Servers: []ServerSummary{}}
	indexes := map[string]int{}
	for _, c := range calls {
		v := s.publicCall(c.Body.View)
		i, ok := indexes[v.ServerID]
		if !ok {
			i = len(out.Servers)
			indexes[v.ServerID] = i
			out.Servers = append(out.Servers, ServerSummary{ServerID: v.ServerID, ServerName: s.scrub(v.ServerName, 256), Facts: []CallFact{}, Missing: []string{"processes", "containers"}})
		}
		f := CallFact{CallID: v.ID, ToolID: v.ToolID, Status: v.Status, Truncated: v.Truncated, Error: s.scrub(v.Error, 256), Excerpt: s.scrub(v.Output, 512)}
		if owners, ok := s.occupants(v); ok {
			f.Occupants = owners
			f.Excerpt = ""
			label := "processes"
			if v.ToolID == "docker_stats" {
				label = "containers"
			}
			missing := out.Servers[i].Missing[:0]
			for _, old := range out.Servers[i].Missing {
				if old != label {
					missing = append(missing, old)
				}
			}
			out.Servers[i].Missing = missing
		}
		if len(v.Output) > 512 {
			f.Truncated = true
		}
		if v.Resources != nil {
			r := *v.Resources
			if len(r.Disks) > 8 {
				r.Disks = r.Disks[:8]
				f.Truncated = true
			}
			r.Disks = append([]DiskResource(nil), r.Disks...)
			for j := range r.Disks {
				r.Disks[j].Filesystem = s.scrub(r.Disks[j].Filesystem, 128)
				r.Disks[j].Mount = s.scrub(r.Disks[j].Mount, 128)
			}
			f.Resources = &r
		}
		out.Servers[i].Facts = append(out.Servers[i].Facts, f)
	}
	return out
}
