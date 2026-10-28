package runretry

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/huangchengsir/pipewright/internal/pipeline"
	"sort"
	"strings"
)

func digest(v any) string {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func nodeKey(stage, job, control string, axes map[string]string) string {
	return digest(struct {
		Version             int
		Stage, Job, Control string
		Axes                map[string]string
	}{SchemaVersion, stage, job, control, axes})
}

// Compile projects both existing scheduler layers without introducing array-order
// dependencies between normal jobs. Control edges and input edges stay distinct.
func Compile(spec pipeline.Spec) (Graph, error) {
	// Bound multiplication before shared expansion allocates the Cartesian set.
	if len(spec.Stages) > 8192 {
		return Graph{}, ErrInvalid
	}
	estimated := 0
	for _, st := range spec.Stages {
		if len(st.Matrix) > pipeline.MatrixMaxAxes {
			return Graph{}, ErrInvalid
		}
		cells := 1
		for _, vals := range st.Matrix {
			if len(vals) == 0 || len(vals) > pipeline.MatrixMaxCells || cells > pipeline.MatrixMaxCells/len(vals) {
				return Graph{}, ErrInvalid
			}
			cells *= len(vals)
		}
		if len(st.Jobs) > 8192 || len(st.Post) > 8192 {
			return Graph{}, ErrInvalid
		}
		estimated += cells * (len(st.Jobs) + len(st.Post) + 8)
		if estimated > 8192 {
			return Graph{}, ErrInvalid
		}
	}
	g := Graph{Version: SchemaVersion, Stages: pipeline.ExpandMatrix(spec.Stages)}
	original := map[string]struct {
		stage pipeline.Stage
		axes  map[string]string
	}{}
	for _, st := range spec.Stages {
		if st.ID == "" {
			return g, ErrInvalid
		}
		cells := []map[string]string{nil}
		if len(st.Matrix) > 0 {
			cells = pipeline.MatrixCells(st.Matrix)
		}
		for _, axes := range cells {
			id := st.ID
			if axes != nil {
				id = pipeline.MatrixCellID(st.ID, axes)
			}
			if _, exists := original[id]; exists {
				return g, fmt.Errorf("%w: matrix execution identity collision", ErrInvalid)
			}
			original[id] = struct {
				stage pipeline.Stage
				axes  map[string]string
			}{st, axes}
		}
	}
	entries, exits := map[string]string{}, map[string]string{}
	jobs := map[string][]NodeRef{}
	add := func(n NodeRef) NodeRef {
		n.Key = nodeKey(n.StageID, n.JobID, n.ControlKey, n.Axes)
		g.Nodes = append(g.Nodes, n)
		return n
	}
	edge := func(a, b, kind string, order int) {
		g.Edges = append(g.Edges, Edge{From: a, To: b, Kind: kind, Order: order})
	}
	for _, st := range g.Stages {
		o := original[st.ID]
		base := NodeRef{StageID: o.stage.ID, Axes: o.axes, ExecutionStageID: st.ID, AllowFailure: st.AllowFailure}
		control := func(key string) NodeRef {
			n := base
			n.ControlKey = key
			n.Name = st.Name
			n.Type = "control"
			return add(n)
		}
		init := control("init")
		entries[st.ID] = init.Key
		prev := init.Key
		if st.Gate {
			n := control("gate")
			edge(prev, n.Key, "control", 0)
			prev = n.Key
		}
		if len(st.Services) > 0 {
			n := control("services")
			edge(prev, n.Key, "control", 0)
			prev = n.Key
		}
		byID := map[string]NodeRef{}
		for i, j := range st.Jobs {
			if j.ID == "" || i >= len(o.stage.Jobs) || o.stage.Jobs[i].ID == "" {
				return g, ErrInvalid
			}
			n := base
			n.JobID = o.stage.Jobs[i].ID
			n.ExecutionJobID = j.ID
			n.Name = j.Name
			n.Type = j.Type
			n = add(n)
			byID[j.ID] = n
			jobs[st.ID] = append(jobs[st.ID], n)
			edge(prev, n.Key, "control", 0)
		}
		for _, j := range st.Jobs {
			needs := append([]string(nil), j.Needs...)
			// This is the real implicit image edge used by runStageJobsDAG.
			if strings.TrimSpace(j.Type) == "push_image" && len(needs) == 0 {
				var candidates []string
				for _, p := range st.Jobs {
					t, _ := p.Config["artifactType"].(string)
					t = strings.TrimSpace(t)
					if strings.TrimSpace(p.Type) == "build_image" && (t == "" || t == "image") {
						candidates = append(candidates, p.ID)
					}
				}
				if len(candidates) == 1 {
					needs = candidates
				}
			}
			for order, id := range needs {
				from, ok := byID[id]
				if !ok || id == j.ID {
					return g, ErrInvalid
				}
				edge(from.Key, byID[j.ID].Key, "control", order)
				// Dynamic env follows original declared needs order, including overwrite order.
				declared := false
				for _, d := range j.Needs {
					if d == id {
						declared = true
					}
				}
				if declared {
					edge(from.Key, byID[j.ID].Key, "env", order)
				}
				edge(from.Key, byID[j.ID].Key, "file", order)
				edge(from.Key, byID[j.ID].Key, "image", order)
			}
		}
		done := control("jobs_done")
		if len(st.Jobs) == 0 {
			edge(prev, done.Key, "control", 0)
		}
		for _, n := range jobs[st.ID] {
			edge(n.Key, done.Key, "control", 0)
		}
		prev = done.Key
		for i := range st.Post {
			n := control(fmt.Sprintf("post:%d", i))
			edge(prev, n.Key, "control", 0)
			prev = n.Key
		}
		cleanup := control("cleanup")
		edge(prev, cleanup.Key, "control", 0)
		barrier := control("barrier")
		edge(cleanup.Key, barrier.Key, "control", 0)
		exits[st.ID] = barrier.Key
	}
	dependencies := pipeline.StageDependencies(g.Stages)
	for _, st := range g.Stages {
		needs := dependencies[st.ID]
		for order, id := range needs {
			if exits[id] == "" || id == st.ID {
				return g, ErrInvalid
			}
			edge(exits[id], entries[st.ID], "control", order)
			// Cross-stage artifact visibility follows stage dependencies, never env.
			for _, from := range jobs[id] {
				for _, to := range jobs[st.ID] {
					edge(from.Key, to.Key, "file", order)
					edge(from.Key, to.Key, "image", order)
				}
			}
		}
	}
	if len(g.Nodes) > 8192 || len(g.Edges) > 131072 {
		return g, ErrInvalid
	}
	order, err := topological(g)
	if err != nil {
		return g, err
	}
	g.Nodes = order
	return g, nil
}
func topological(g Graph) ([]NodeRef, error) {
	by := map[string]NodeRef{}
	count := map[string]int{}
	next := map[string][]string{}
	for _, n := range g.Nodes {
		if _, ok := by[n.Key]; ok {
			return nil, ErrInvalid
		}
		by[n.Key] = n
	}
	seen := map[string]bool{}
	for _, e := range g.Edges {
		if _, ok := by[e.From]; !ok {
			return nil, ErrInvalid
		}
		if _, ok := by[e.To]; !ok {
			return nil, ErrInvalid
		}
		k := e.From + e.To
		if !seen[k] {
			seen[k] = true
			count[e.To]++
			next[e.From] = append(next[e.From], e.To)
		}
	}
	var ready []string
	for _, n := range g.Nodes {
		if count[n.Key] == 0 {
			ready = append(ready, n.Key)
		}
	}
	var out []NodeRef
	for len(ready) > 0 {
		sort.Strings(ready)
		k := ready[0]
		ready = ready[1:]
		out = append(out, by[k])
		for _, to := range next[k] {
			count[to]--
			if count[to] == 0 {
				ready = append(ready, to)
			}
		}
	}
	if len(out) != len(g.Nodes) {
		return nil, ErrInvalid
	}
	return out, nil
}
