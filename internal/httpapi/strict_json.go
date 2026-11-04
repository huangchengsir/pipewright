package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
)

var errStrictJSON = errors.New("invalid JSON object")

func strictObjectJSON(raw []byte, out any, limit int) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' || len(raw) > limit {
		return errStrictJSON
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var walk func(int) error
	walk = func(depth int) error {
		if depth > 16 {
			return errStrictJSON
		}
		t, err := d.Token()
		if err != nil {
			return err
		}
		if delim, ok := t.(json.Delim); ok {
			switch delim {
			case '{':
				seen := map[string]bool{}
				for d.More() {
					k, e := d.Token()
					if e != nil {
						return e
					}
					key, ok := k.(string)
					if !ok || seen[strings.ToLower(key)] {
						return errStrictJSON
					}
					seen[strings.ToLower(key)] = true
					if e = walk(depth + 1); e != nil {
						return e
					}
				}
			case '[':
				for d.More() {
					if e := walk(depth + 1); e != nil {
						return e
					}
				}
			default:
				return errStrictJSON
			}
			_, err = d.Token()
			return err
		}
		return nil
	}
	if err := walk(0); err != nil {
		return errStrictJSON
	}
	if _, err := d.Token(); err != io.EOF {
		return errStrictJSON
	}
	d = json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(out) != nil {
		return errStrictJSON
	}
	return nil
}
