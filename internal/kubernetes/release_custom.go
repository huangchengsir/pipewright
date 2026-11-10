package kubernetes

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"gopkg.in/yaml.v3"
	"io"
	"strings"
	"time"
)

func (d *ReleaseDiscovery) addProposedCRDs(files []ReleaseInputFile, vars map[string]ReleaseVariableValue, render bool) error {
	if d.proposed == nil {
		d.proposed = map[string]ReleaseResourceID{}
	}
	for _, file := range files {
		decoder := yaml.NewDecoder(bytes.NewReader(file.Content))
		for {
			var node yaml.Node
			e := decoder.Decode(&node)
			if e == io.EOF {
				break
			}
			if e != nil {
				return ErrManifest
			}
			if len(node.Content) == 0 || node.Content[0].Tag == "!!null" {
				continue
			}
			count := 0
			sensitive := false
			if render {
				e = renderReleaseNode(&node, vars, &sensitive, 0, &count)
			} else {
				e = renderReleaseNodeLiteral(&node, 0, &count)
			}
			if e != nil {
				return e
			}
			var object map[string]any
			if node.Decode(&object) != nil {
				return ErrManifest
			}
			if object["apiVersion"] != "apiextensions.k8s.io/v1" || object["kind"] != "CustomResourceDefinition" {
				continue
			}
			spec := releaseMap(object["spec"])
			group := releaseString(spec["group"])
			names := releaseMap(spec["names"])
			kind, plural := releaseString(names["kind"]), releaseString(names["plural"])
			scope := releaseString(spec["scope"])
			name := releaseString(releaseMap(object["metadata"])["name"])
			if !validDeploymentName(group) || !validDeploymentName(plural) || kind == "" || name != plural+"."+group || scope != "Namespaced" && scope != "Cluster" {
				return ErrDiscovery
			}
			id := ReleaseResourceID{Group: "apiextensions.k8s.io", Version: "v1", Resource: "customresourcedefinitions", Kind: "CustomResourceDefinition", Name: name}
			served := 0
			for _, value := range releaseItems(spec["versions"]) {
				version := releaseMap(value)
				if version["served"] != true {
					continue
				}
				v := releaseString(version["name"])
				schema := releaseMap(releaseMap(version["schema"])["openAPIV3Schema"])
				if !validID(v) || len(schema) == 0 {
					return ErrDiscovery
				}
				key := group + "/" + v + "/" + kind
				entry := ReleaseAPIResource{Group: group, Version: v, Name: plural, Kind: kind, Namespaced: scope == "Namespaced", Verbs: []string{"get", "create", "patch", "update", "delete"}}
				if previous, ok := d.resources[key]; ok && (previous.Name != entry.Name || previous.Namespaced != entry.Namespaced) {
					return ErrDiscovery
				}
				d.resources[key] = entry
				d.proposed[key] = id
				served++
			}
			if served == 0 {
				return ErrDiscovery
			}
		}
	}
	return nil
}
func releaseSchemaPath(id ReleaseResourceID) string {
	if id.Group == "" {
		return "/openapi/v3/api/" + id.Version
	}
	return "/openapi/v3/apis/" + id.Group + "/" + id.Version
}
func releaseSchemaAvailable(raw []byte, id ReleaseResourceID) bool {
	var document map[string]any
	if len(raw) == 0 || len(raw) > MaxReleaseManifestBytes || json.Unmarshal(raw, &document) != nil {
		return false
	}
	for _, schema := range releaseMap(releaseMap(document["components"])["schemas"]) {
		for _, value := range releaseItems(releaseMap(schema)["x-kubernetes-group-version-kind"]) {
			kind := releaseMap(value)
			if releaseString(kind["group"]) == id.Group && kind["version"] == id.Version && kind["kind"] == id.Kind {
				return true
			}
		}
	}
	return false
}

var errCRDRegistrationPending = errors.New("kubernetes_crd_registration_pending")

func (c *ReleaseController) verifyCustomPrerequisites(ctx context.Context, api releaseAPI, parent ReleaseRecord, resource ReleaseResource) error {
	for {
		e := c.verifyCustomPrerequisitesOnce(ctx, api, parent, resource)
		if !errors.Is(e, errCRDRegistrationPending) {
			return e
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(c.poll):
		}
	}
}
func (c *ReleaseController) verifyCustomPrerequisitesOnce(ctx context.Context, api releaseAPI, parent ReleaseRecord, resource ReleaseResource) error {
	plan := parent.Plan
	if resource.RequiredCRD != nil {
		found := false
		for _, definition := range plan.Resources {
			if definition.ID == *resource.RequiredCRD {
				current, e := readReleaseObject(ctx, api, definition.ID)
				if e != nil {
					return e
				}
				if definition.VerifyOnly {
					if definition.ExpectedUID == "" || releaseMap(current["metadata"])["uid"] != definition.ExpectedUID {
						return ErrSuperseded
					}
					for _, baseline := range plan.Baseline {
						if baseline.ID == definition.ID && !releaseBaselineMatches(baseline.Object, current) {
							return ErrSuperseded
						}
					}
				} else {
					facts, e := c.store.ListReleaseActions(ctx, parent.ID)
					if e != nil {
						return e
					}
					owned := false
					for _, fact := range facts {
						if fact.Intent.Resource == definition.ID && fact.Intent.Verb != "delete" && fact.State == "verified" && fact.Receipt != nil && releaseMap(current["metadata"])["uid"] == fact.Receipt.UID && releaseMap(releaseMap(current["metadata"])["annotations"])[ReleaseActionAnnotation] == fact.ID && releaseResourceBaselineMatches(definition, fact.Receipt.Object, current) {
							owned = true
						}
					}
					if !owned {
						return ErrSuperseded
					}
				}
				health := EvaluateReleaseHealth(definition, current, nil)
				if health.State == "failed" || health.State == "unverified" {
					return ErrRollout
				}
				if health.Reason == "approved_fields_not_observed" {
					return ErrSuperseded
				}
				if health.State != "ready" {
					return errCRDRegistrationPending
				}
				found = true
				break
			}
		}
		if !found {
			return ErrDiscovery
		}
		raw, e := api.Read(ctx, "/apis/"+resource.ID.Group+"/"+resource.ID.Version, "")
		if errors.Is(e, ErrNotFound) {
			return errCRDRegistrationPending
		}
		if e != nil {
			return e
		}
		d := NewReleaseDiscovery()
		e = d.Add(raw)
		clear(raw)
		if e != nil {
			return e
		}
		actual, e := d.Resolve(resource.ID.Group+"/"+resource.ID.Version, resource.ID.Kind)
		if e != nil {
			return errCRDRegistrationPending
		}
		if actual.Name != resource.ID.Resource || actual.Namespaced != (resource.ID.Namespace != "") {
			return ErrDiscovery
		}
	}
	raw, e := api.Read(ctx, releaseSchemaPath(resource.ID), "")
	if errors.Is(e, ErrNotFound) && resource.RequiredCRD != nil {
		return errCRDRegistrationPending
	}
	if e != nil {
		return e
	}
	defer clear(raw)
	if !releaseSchemaAvailable(raw, resource.ID) && resource.RequiredCRD != nil {
		return errCRDRegistrationPending
	}
	if !releaseSchemaAvailable(raw, resource.ID) || resource.SchemaDigest != "" && resource.SchemaDigest != releaseByteDigest(raw) {
		return ErrDiscovery
	}
	return nil
}

type ReleasePredicate struct {
	Predicate string `json:"predicate"`
	Expected  string `json:"expected"`
}

func releasePredicateValue(object map[string]any, predicate string) (any, int64, bool) {
	if strings.HasPrefix(predicate, "condition:") {
		name := strings.TrimPrefix(predicate, "condition:")
		if name == "" || len(name) > 128 {
			return nil, 0, false
		}
		for _, value := range releaseItems(releaseMap(object["status"])["conditions"]) {
			condition := releaseMap(value)
			if condition["type"] == name {
				return condition["status"], releaseInt(condition["observedGeneration"]), true
			}
		}
		return nil, 0, false
	}
	if !strings.HasPrefix(predicate, "/status/") || len(predicate) > 256 {
		return nil, 0, false
	}
	var current any = object
	for _, key := range strings.Split(strings.TrimPrefix(predicate, "/"), "/") {
		key = strings.ReplaceAll(strings.ReplaceAll(key, "~1", "/"), "~0", "~")
		mapping, ok := current.(map[string]any)
		if !ok {
			return nil, 0, false
		}
		value, ok := mapping[key]
		if !ok {
			return nil, 0, false
		}
		current = value
	}
	return current, releaseInt(releaseMap(object["status"])["observedGeneration"]), true
}
func evaluateReleasePredicates(resource ReleaseResource, actual map[string]any) ReleaseHealth {
	if len(resource.Predicates) == 0 {
		return ReleaseHealth{State: "unverified", Reason: "custom_health_predicate_required"}
	}
	generation := releaseInt(releaseMap(actual["metadata"])["generation"])
	for _, predicate := range resource.Predicates {
		value, observed, ok := releasePredicateValue(actual, predicate.Predicate)
		if !ok {
			return ReleaseHealth{State: "pending", Reason: "custom_condition_not_observed"}
		}
		if observed == 0 {
			observed = releaseInt(releaseMap(actual["status"])["observedGeneration"])
		}
		if generation > 0 && observed < generation {
			return ReleaseHealth{State: "pending", Reason: "custom_generation_not_observed"}
		}
		encoded, _ := json.Marshal(value)
		matches := string(encoded) == predicate.Expected || releaseString(value) == predicate.Expected
		if !matches {
			return ReleaseHealth{State: "pending", Reason: "custom_condition_not_ready"}
		}
	}
	return ReleaseHealth{State: "ready", Reason: "custom_conditions_verified"}
}
