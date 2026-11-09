package kubernetes

import (
	"encoding/json"
	"math"
	"math/big"
	"reflect"
	"strconv"
	"strings"
)

func releaseNumberCandidate(v any) bool {
	if _, ok := v.(json.Number); ok {
		return true
	}
	if v == nil {
		return false
	}
	k := reflect.TypeOf(v).Kind()
	return k >= reflect.Int && k <= reflect.Float64
}

// Compare JSON numeric values, never their formatting against another JSON
// type. Floats outside the consecutive-integer range cannot prove exact input
// identity after generic JSON decoding and conservatively fail closed.
func releaseJSONNumber(v any) (*big.Rat, bool) {
	var s string
	if n, ok := v.(json.Number); ok {
		s = string(n)
	} else {
		if v == nil {
			return nil, false
		}
		n := reflect.ValueOf(v)
		switch n.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			s = strconv.FormatInt(n.Int(), 10)
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			s = strconv.FormatUint(n.Uint(), 10)
		case reflect.Float32, reflect.Float64:
			f := n.Float()
			limit := math.Exp2(53)
			bits := 64
			if n.Kind() == reflect.Float32 {
				limit = math.Exp2(24)
				bits = 32
			}
			if math.IsNaN(f) || math.IsInf(f, 0) || math.Abs(f) >= limit {
				return nil, false
			}
			s = strconv.FormatFloat(f, 'g', -1, bits)
		default:
			return nil, false
		}
	}
	if len(s) == 0 || len(s) > 256 || !json.Valid([]byte(s)) || s[0] == '"' || s == "null" || s == "true" || s == "false" {
		return nil, false
	}
	if i := strings.IndexAny(s, "eE"); i >= 0 {
		exponent, e := strconv.Atoi(s[i+1:])
		if e != nil || exponent < -10000 || exponent > 10000 {
			return nil, false
		}
	}
	n, ok := new(big.Rat).SetString(s)
	return n, ok
}
func releaseScalarMatches(want, actual any) bool {
	if releaseNumberCandidate(want) || releaseNumberCandidate(actual) {
		w, wok := releaseJSONNumber(want)
		a, aok := releaseJSONNumber(actual)
		return wok && aok && w.Cmp(a) == 0
	}
	return reflect.DeepEqual(want, actual)
}
