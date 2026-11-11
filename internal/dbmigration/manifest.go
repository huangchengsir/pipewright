// Package dbmigration implements the data and durable-control portions of an
// explicitly requested, same-instance database migration. It performs no runtime
// connection swapping and never enables business writes by itself.
package dbmigration

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
)

type Kind string

const (
	Integer Kind = "integer"
	Float   Kind = "float"
	Text    Kind = "text"
	Binary  Kind = "binary"
)

type Column struct {
	Name     string
	Kind     Kind
	Nullable bool
	Key      int
}
type ForeignKey struct{ Column, Table, Referenced, OnUpdate, OnDelete string }
type Table struct {
	Name        string
	Columns     []Column
	ForeignKeys []ForeignKey
	SeedRows    [][]any
}

var (
	ErrUnsupported    = errors.New("dbmigration: unsupported database capability")
	ErrTargetNotEmpty = errors.New("dbmigration: target must be a dedicated empty database or schema")
	ErrMismatch       = errors.New("dbmigration: data or receipt verification failed")
	ErrCommitUnknown  = errors.New("dbmigration: commit outcome unknown; inspect before explicit continuation")
	ErrIdentity       = errors.New("dbmigration: database identity mismatch")
	ErrInvalidState   = errors.New("dbmigration: invalid migration state")
)

// Manifest returns private copies in foreign-key dependency order.
func Manifest() []Table {
	pending := make(map[string]Table, len(schemaTables))
	for _, table := range schemaTables {
		pending[table.Name] = table
	}
	out := make([]Table, 0, len(pending))
	for len(pending) > 0 {
		names := make([]string, 0, len(pending))
		for name := range pending {
			names = append(names, name)
		}
		sort.Strings(names)
		progressed := false
		for _, name := range names {
			table := pending[name]
			ready := true
			for _, fk := range table.ForeignKeys {
				if _, wait := pending[fk.Table]; wait && fk.Table != name {
					ready = false
					break
				}
			}
			if !ready {
				continue
			}
			table.Columns = append([]Column(nil), table.Columns...)
			table.ForeignKeys = append([]ForeignKey(nil), table.ForeignKeys...)
			seeds := make([][]any, len(table.SeedRows))
			for i, row := range table.SeedRows {
				seeds[i] = append([]any(nil), row...)
			}
			table.SeedRows = seeds
			out = append(out, table)
			delete(pending, name)
			progressed = true
		}
		if !progressed {
			panic("dbmigration: cyclic manifest requires an explicit copy strategy")
		}
	}
	return out
}
func ManifestDigest() string {
	b, err := json.Marshal(Manifest())
	if err != nil {
		panic(err)
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func (t Table) keys() []int {
	var keys []int
	for i, col := range t.Columns {
		if col.Key > 0 {
			keys = append(keys, i)
		}
	}
	sort.Slice(keys, func(i, j int) bool { return t.Columns[keys[i]].Key < t.Columns[keys[j]].Key })
	return keys
}
