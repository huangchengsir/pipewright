package store

import "strings"

// These two DDL engines commit column additions independently of migration
// bookkeeping. A restart after either addition must still finish 0068.
func (s *Store) pipelineHistoryAlterApplied(stmt string) (bool, error) {
	var table string
	switch stmt = strings.TrimSpace(stmt); stmt {
	case "ALTER TABLE pipeline_configs ADD COLUMN history_version_id VARCHAR(64) NULL",
		`ALTER TABLE "PIPELINE_CONFIGS" ADD "HISTORY_VERSION_ID" VARCHAR(64 CHAR);`:
		table = "pipeline_configs"
	case "ALTER TABLE pipeline_runs ADD COLUMN history_version_id VARCHAR(64) NULL",
		`ALTER TABLE "PIPELINE_RUNS" ADD "HISTORY_VERSION_ID" VARCHAR(64 CHAR);`:
		table = "pipeline_runs"
	default:
		return false, nil
	}
	var count int
	if s.Dialect == DM {
		err := s.DB.QueryRow(`SELECT COUNT(*) FROM SYS.SYSCOLUMNS c
		 JOIN SYS.SYSOBJECTS o ON o.ID=c.ID
		 WHERE o.SCHID=CURRENT_SCHID() AND o.SUBTYPE$='UTAB'
		   AND UPPER(o.NAME)=? AND UPPER(c.NAME)='HISTORY_VERSION_ID'`, strings.ToUpper(table)).Scan(&count)
		return count > 0, err
	}
	err := s.DB.QueryRow(`SELECT COUNT(*) FROM information_schema.COLUMNS
	  WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME=? AND COLUMN_NAME='history_version_id'`, table).Scan(&count)
	return count > 0, err
}
