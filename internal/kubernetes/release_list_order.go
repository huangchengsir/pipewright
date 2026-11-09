package kubernetes

import "container/heap"

type releaseOrderQueue []int

func (q releaseOrderQueue) Len() int           { return len(q) }
func (q releaseOrderQueue) Less(i, j int) bool { return q[i] < q[j] }
func (q releaseOrderQueue) Swap(i, j int)      { q[i], q[j] = q[j], q[i] }
func (q *releaseOrderQueue) Push(v any)        { *q = append(*q, v.(int)) }
func (q *releaseOrderQueue) Pop() any {
	old := *q
	v := old[len(old)-1]
	*q = old[:len(old)-1]
	return v
}

// Desired members set their complete relative order. Unowned live members keep
// their mutual order and their before/after relation to every surviving old
// owned member. New desired members take the earliest legal position. A cycle
// means a genuine external ordering conflict; never move/drop an injection to
// satisfy it. With no injections this is exactly the requested desired order.
func releaseOrderNativeMembers(field string, old, live, next, result []any) ([]any, error) {
	prior := map[string]bool{}
	for _, v := range old {
		k, _ := releaseTemplateListKey(field, v)
		prior[k] = true
	}
	values := map[string]any{}
	for _, v := range result {
		k, _ := releaseTemplateListKey(field, v)
		values[k] = v
	}
	keys := []string{}
	rank := map[string]int{}
	add := func(k string) {
		if _, ok := rank[k]; !ok {
			rank[k] = len(keys)
			keys = append(keys, k)
		}
	}
	for _, v := range next {
		k, _ := releaseTemplateListKey(field, v)
		add(k)
	}
	for _, v := range live {
		k, _ := releaseTemplateListKey(field, v)
		if values[k] != nil {
			add(k)
		}
	}
	edges := make([][]int, len(keys))
	degree := make([]int, len(keys))
	seen := map[[2]int]bool{}
	edge := func(a, b string) {
		if a == "" || b == "" || a == b {
			return
		}
		x, y := rank[a], rank[b]
		pair := [2]int{x, y}
		if !seen[pair] {
			edges[x] = append(edges[x], y)
			degree[y]++
			seen[pair] = true
		}
	}
	last := ""
	for _, v := range next {
		k, _ := releaseTemplateListKey(field, v)
		edge(last, k)
		last = k
	}
	unowned := ""
	segment := []string{}
	for _, v := range live {
		k, _ := releaseTemplateListKey(field, v)
		if values[k] == nil {
			continue
		}
		if prior[k] {
			edge(unowned, k)
			segment = append(segment, k)
		} else {
			edge(unowned, k)
			for _, p := range segment {
				edge(p, k)
			}
			segment = segment[:0]
			unowned = k
		}
	}
	q := &releaseOrderQueue{}
	for i, n := range degree {
		if n == 0 {
			heap.Push(q, i)
		}
	}
	out := []any{}
	for q.Len() > 0 {
		i := heap.Pop(q).(int)
		out = append(out, values[keys[i]])
		for _, j := range edges[i] {
			degree[j]--
			if degree[j] == 0 {
				heap.Push(q, j)
			}
		}
	}
	if len(out) != len(result) {
		return nil, ErrSuperseded
	}
	return out, nil
}
