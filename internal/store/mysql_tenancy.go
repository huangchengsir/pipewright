package store

import (
	"fmt"
	"strings"
)

// Legacy IDs use the collation chosen when each parent table was created.
// Copy that exact column storage into only the new referencing columns; never
// rewrite existing IDs or assume the database/server default is still the same.
func (s *Store) mysqlTenancyForeignKeys(sqlText string) (string, error) {
	columns := make(map[string]string, 2)
	for _, pair := range [][2]string{{"run_id", "pipeline_runs"}, {"project_id", "projects"}} {
		var dataType, charset, collation string
		var length int
		err := s.DB.QueryRow(`SELECT DATA_TYPE, CHARACTER_MAXIMUM_LENGTH, CHARACTER_SET_NAME, COLLATION_NAME FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ? AND COLUMN_NAME = 'id'`, pair[1]).Scan(&dataType, &length, &charset, &collation)
		if err != nil {
			return "", fmt.Errorf("read legacy %s ID storage: %w", pair[1], err)
		}
		definition, err := mysqlLegacyIDDefinition(dataType, length, charset, collation)
		if err != nil {
			return "", fmt.Errorf("legacy %s ID storage: %w", pair[1], err)
		}
		columns[pair[0]] = definition
	}
	const table = "CREATE TABLE access_run_authorizations ("
	before, body, ok := strings.Cut(sqlText, table)
	if !ok {
		return "", fmt.Errorf("missing run authorization migration")
	}
	for _, column := range []string{"run_id", "project_id"} {
		old := "\n " + column + " VARCHAR(64) NOT NULL,"
		if strings.Count(body, old) != 1 {
			return "", fmt.Errorf("unexpected %s migration declaration", column)
		}
		body = strings.Replace(body, old, "\n "+column+" "+columns[column]+",", 1)
	}
	return before + table + body, nil
}

func mysqlLegacyIDDefinition(dataType string, length int, charset, collation string) (string, error) {
	if dataType != "varchar" || length != 64 || charset != "utf8mb4" || !strings.HasPrefix(collation, "utf8mb4_") || len(collation) > 64 {
		return "", fmt.Errorf("unsupported parent column storage")
	}
	// These metadata identifiers enter DDL, so accept only MySQL identifier bytes.
	for _, c := range collation {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_') {
			return "", fmt.Errorf("invalid parent collation")
		}
	}
	return "VARCHAR(64) CHARACTER SET utf8mb4 COLLATE " + collation + " NOT NULL", nil
}
