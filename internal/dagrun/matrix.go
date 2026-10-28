package dagrun

import "github.com/huangchengsir/pipewright/internal/pipeline"

// ExpandMatrix delegates to the shared canonical expansion used by recovery planning.
func ExpandMatrix(stages []pipeline.Stage) []pipeline.Stage { return pipeline.ExpandMatrix(stages) }
