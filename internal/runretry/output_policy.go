package runretry

import (
	"encoding/json"
	"github.com/huangchengsir/pipewright/internal/pipeline"
	"time"
)

// checkpoint:{reusable:false,ttlSeconds:60} is an explicit output policy. An
// invalid policy loses recoverability, never the result of completed work.
func ApplyOutputPolicy(job pipeline.Job, o *Outcome, now time.Time) {
	raw, exists := job.Config["checkpoint"]
	if !exists {
		return
	}
	var policy struct {
		Reusable   *bool  `json:"reusable"`
		TTLSeconds *int64 `json:"ttlSeconds"`
	}
	data, err := json.Marshal(raw)
	if err == nil {
		err = json.Unmarshal(data, &policy)
	}
	if err != nil || policy.TTLSeconds != nil && (*policy.TTLSeconds <= 0 || *policy.TTLSeconds > 31536000) {
		o.Complete = false
		o.Reusable = false
		o.ReasonCode = "checkpoint_policy_invalid"
		return
	}
	if policy.Reusable != nil && !*policy.Reusable {
		o.Reusable = false
		o.ReasonCode = "checkpoint_nonreusable"
	}
	if policy.TTLSeconds != nil {
		expiry := now.Add(time.Duration(*policy.TTLSeconds) * time.Second)
		o.ValidUntil = &expiry
	}
}
