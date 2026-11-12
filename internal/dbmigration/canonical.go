package dbmigration

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash"
	"math"
	"strconv"
	"unicode/utf8"
)

// Canonical values distinguish NULL, empty text and empty bytes. Numbers never
// pass through JSON floating-point conversion; integer keys retain all 64 bits.
func normalize(col Column, value any) (any, error) {
	if value == nil {
		if !col.Nullable {
			return nil, fmt.Errorf("%w: required column %s is NULL", ErrMismatch, col.Name)
		}
		return nil, nil
	}
	switch col.Kind {
	case Integer:
		switch v := value.(type) {
		case int64:
			return v, nil
		case int:
			return int64(v), nil
		case bool:
			if v {
				return int64(1), nil
			}
			return int64(0), nil
		case []byte:
			return parseInteger(string(v))
		case string:
			return parseInteger(v)
		}
	case Float:
		var n float64
		switch v := value.(type) {
		case float64:
			n = v
		case int64:
			n = float64(v)
		case []byte:
			x, e := strconv.ParseFloat(string(v), 64)
			if e != nil {
				return nil, ErrMismatch
			}
			n = x
		case string:
			x, e := strconv.ParseFloat(v, 64)
			if e != nil {
				return nil, ErrMismatch
			}
			n = x
		default:
			return nil, ErrMismatch
		}
		if math.IsNaN(n) || math.IsInf(n, 0) {
			return nil, errors.New("dbmigration: non-finite numeric value requires explicit handling")
		}
		return n, nil
	case Text:
		var text string
		switch v := value.(type) {
		case string:
			text = v
		case []byte:
			text = string(v)
		default:
			return nil, ErrMismatch
		}
		if !utf8.ValidString(text) {
			return nil, errors.New("dbmigration: invalid UTF-8 text")
		}
		return text, nil
	case Binary:
		if v, ok := value.([]byte); ok {
			return append([]byte{}, v...), nil
		}
	}
	return nil, fmt.Errorf("%w: unexpected value type for column %s", ErrMismatch, col.Name)
}
func normalizeRow(table Table, values []any) ([]any, error) {
	if len(values) != len(table.Columns) {
		return nil, ErrMismatch
	}
	row := make([]any, len(values))
	for i, value := range values {
		n, e := normalize(table.Columns[i], value)
		if e != nil {
			return nil, e
		}
		row[i] = n
	}
	return row, nil
}
func hashField(h hash.Hash, b []byte) {
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(b)))
	_, _ = h.Write(size[:])
	_, _ = h.Write(b)
}
func hashRow(h hash.Hash, table Table, row []any) {
	hashField(h, []byte(table.Name))
	for i, value := range row {
		hashField(h, []byte(table.Columns[i].Name))
		hashField(h, []byte(table.Columns[i].Kind))
		if value == nil {
			_, _ = h.Write([]byte{0})
			continue
		}
		_, _ = h.Write([]byte{1})
		switch v := value.(type) {
		case int64:
			var b [8]byte
			binary.BigEndian.PutUint64(b[:], uint64(v))
			hashField(h, b[:])
		case float64:
			var b [8]byte
			binary.BigEndian.PutUint64(b[:], math.Float64bits(v))
			hashField(h, b[:])
		case string:
			hashField(h, []byte(v))
		case []byte:
			hashField(h, v)
		default:
			panic("dbmigration: non-canonical hash value")
		}
	}
}

func parseInteger(value string) (any, error) {
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return nil, ErrMismatch
	}
	return n, nil
}
