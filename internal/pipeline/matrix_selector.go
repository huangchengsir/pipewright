package pipeline

import (
	"encoding/json"
	"reflect"
)

// Cross-stage selectors resolve only from the complete exact source axis tuple.
// A missing axis or multiple candidate is an explicit preparation error.
func remapMatrixArtifactSelectors(stage Stage, axes map[string]string, originals map[string]Stage) Stage {
	stage.Jobs = append([]Job(nil), stage.Jobs...)
	for i, job := range stage.Jobs {
		text, ok := job.Config["artifactSource"].(string)
		if !ok || text == "" {
			continue
		}
		var selector map[string]json.RawMessage
		if json.Unmarshal([]byte(text), &selector) != nil {
			continue
		}
		var sourceStage, sourceJob string
		_ = json.Unmarshal(selector["stageId"], &sourceStage)
		_ = json.Unmarshal(selector["jobId"], &sourceJob)
		source, known := originals[sourceStage]
		if !known || len(source.Matrix) == 0 {
			continue
		}
		knownJob := false
		for _, candidate := range source.Jobs {
			knownJob = knownJob || candidate.ID == sourceJob
		}
		var matches []map[string]string
		if knownJob {
			for _, cell := range cartesian(source.Matrix) {
				match := true
				for axis, value := range cell {
					actual, present := axes[axis]
					if !present || actual != value {
						match = false
						break
					}
				}
				if match {
					matches = append(matches, cell)
				}
			}
		}
		cfg := map[string]any{}
		for key, value := range job.Config {
			if value != nil {
				cfg[key] = cloneMatrixValue(reflect.ValueOf(value)).Interface()
			} else {
				cfg[key] = nil
			}
		}
		if len(matches) != 1 {
			cfg["__matrixArtifactSourceAmbiguous"] = true
		} else {
			cell := matches[0]
			selector["stageId"], _ = json.Marshal(cellID(source.ID, cell))
			selector["jobId"], _ = json.Marshal(sourceJob + "__" + cellSuffixFromCell(cell))
			data, _ := json.Marshal(selector)
			cfg["artifactSource"] = string(data)
		}
		job.Config = cfg
		stage.Jobs[i] = job
	}
	return stage
}
