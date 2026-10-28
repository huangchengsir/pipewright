package runretry

// JobDependencies uses only captured scheduler control edges. It deliberately
// leaves declared Job.Needs unchanged because those govern dynamic env flow.
func (s Snapshot) JobDependencies(stage string) (map[string][]string, error) {
	if s.Compiled == nil {
		return nil, ErrUnavailable
	}
	keys := map[string]string{}
	out := map[string][]string{}
	for _, n := range s.Compiled.Nodes {
		if n.ExecutionStageID == stage && n.ControlKey == "" {
			keys[n.Key] = n.ExecutionJobID
			out[n.ExecutionJobID] = []string{}
		}
	}
	for _, edge := range s.Compiled.Edges {
		if edge.Kind != "control" {
			continue
		}
		from, fok := keys[edge.From]
		to, tok := keys[edge.To]
		if fok && tok {
			out[to] = append(out[to], from)
		}
	}
	return out, nil
}
