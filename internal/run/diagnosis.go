package run

import (
	"encoding/json"
	"strings"
	"time"
)

// diagnosis.go 是 Diagnosis 领域类型的持久化编解码(pipeline_runs.diagnosis_json)。
//
// 落库 JSON 字段名与冻结 run-detail diagnosis 子 DTO 对齐(camelCase),便于审计/排查;
// 但 run 包不负责 HTTP 输出(httpapi 层另有 DTO 映射)。空串 = 未诊断 → nil。
//
// **铁律**:写入此列的 Diagnosis 必由调用方在出网前脱敏(evidence 取自脱敏后日志);
// 本编解码不做脱敏(职责单一),也绝不引入明文 secret。

// diagnosisRow 是 Diagnosis 的可序列化镜像(字段名对齐冻结 DTO)。
type diagnosisRow struct {
	Status          string                 `json:"status"`
	Reason          string                 `json:"reason"`
	Hypothesis      string                 `json:"hypothesis"`
	Confidence      string                 `json:"confidence"`
	AlternateCauses []string               `json:"alternateCauses"`
	FixSuggestions  []string               `json:"fixSuggestions"`
	FixScript       string                 `json:"fixScript,omitempty"`
	Evidence        []diagnosisEvidenceRow `json:"evidence"`
	GeneratedAt     string                 `json:"generatedAt"`
}

type diagnosisEvidenceRow struct {
	Line      int    `json:"line"`
	Text      string `json:"text"`
	Highlight bool   `json:"highlight"`
}

// encodeDiagnosis 把 Diagnosis 序列化为可入库的 JSON 字符串(nil → 空串,表未诊断)。
func encodeDiagnosis(d *Diagnosis) (string, error) {
	if d == nil {
		return "", nil
	}
	alt := d.AlternateCauses
	if alt == nil {
		alt = []string{}
	}
	fixes := d.FixSuggestions
	if fixes == nil {
		fixes = []string{}
	}
	ev := make([]diagnosisEvidenceRow, 0, len(d.Evidence))
	for _, e := range d.Evidence {
		ev = append(ev, diagnosisEvidenceRow{Line: e.Line, Text: e.Text, Highlight: e.Highlight})
	}
	row := diagnosisRow{
		Status:          d.Status,
		Reason:          d.Reason,
		Hypothesis:      d.Hypothesis,
		Confidence:      d.Confidence,
		AlternateCauses: alt,
		FixSuggestions:  fixes,
		FixScript:       d.FixScript,
		Evidence:        ev,
		GeneratedAt:     d.GeneratedAt.UTC().Format(time.RFC3339),
	}
	b, err := json.Marshal(row)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// decodeDiagnosis 把入库 JSON 反序列化为 *Diagnosis(空串/无效 → nil, nil,视为未诊断,
// 绝不让坏数据致 Get 失败)。
func decodeDiagnosis(s string) (*Diagnosis, error) {
	if strings.TrimSpace(s) == "" {
		return nil, nil
	}
	var row diagnosisRow
	if err := json.Unmarshal([]byte(s), &row); err != nil {
		// 坏数据容错:视为未诊断,不阻断 run-detail。
		return nil, nil
	}
	ev := make([]DiagnosisEvidence, 0, len(row.Evidence))
	for _, e := range row.Evidence {
		ev = append(ev, DiagnosisEvidence{Line: e.Line, Text: e.Text, Highlight: e.Highlight})
	}
	d := &Diagnosis{
		Status:          row.Status,
		Reason:          row.Reason,
		Hypothesis:      row.Hypothesis,
		Confidence:      row.Confidence,
		AlternateCauses: row.AlternateCauses,
		FixSuggestions:  row.FixSuggestions,
		FixScript:       row.FixScript,
		Evidence:        ev,
	}
	if t, perr := time.Parse(time.RFC3339, row.GeneratedAt); perr == nil {
		d.GeneratedAt = t.UTC()
	}
	return d, nil
}
