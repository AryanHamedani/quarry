package wp

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
)

// FlexString unmarshals a JSON string, number, or boolean safely into a Go string.
// In PHP/WordPress, functions that fail often return `false` (serialized as JSON `false`)
// or numeric values where a string is expected by the schema (e.g., `mime_type: false`).
// When PHP returns `false` or `null`, FlexString normalizes it to `""`.
type FlexString string

func (s *FlexString) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || string(data) == "null" || string(data) == "false" {
		*s = ""
		return nil
	}
	if data[0] == '"' {
		var str string
		if err := json.Unmarshal(data, &str); err != nil {
			*s = ""
			return nil
		}
		*s = FlexString(str)
		return nil
	}
	if string(data) == "true" {
		*s = "true"
		return nil
	}
	// If it's a number, keep its string representation; if it's an unexpected array/object, ignore gracefully.
	if (data[0] >= '0' && data[0] <= '9') || data[0] == '-' {
		*s = FlexString(string(data))
		return nil
	}
	*s = ""
	return nil
}

func (s FlexString) String() string {
	return string(s)
}

// FlexInt unmarshals a JSON integer, float, numeric string (`"1024"`), or PHP `false`/`null`/`""` into an int.
type FlexInt int

func (n *FlexInt) UnmarshalJSON(data []byte) error {
	v, err := parseFlexInt64(data)
	if err != nil {
		*n = 0
		return nil
	}
	*n = FlexInt(v)
	return nil
}

// FlexInt64 unmarshals a JSON integer, float, numeric string (`"482910"`), or PHP `false`/`null`/`""` into an int64.
type FlexInt64 int64

func (n *FlexInt64) UnmarshalJSON(data []byte) error {
	v, err := parseFlexInt64(data)
	if err != nil {
		*n = 0
		return nil
	}
	*n = FlexInt64(v)
	return nil
}

func parseFlexInt64(data []byte) (int64, error) {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || string(data) == "null" || string(data) == "false" {
		return 0, nil
	}
	if string(data) == "true" {
		return 1, nil
	}
	if data[0] == '"' {
		var str string
		if err := json.Unmarshal(data, &str); err != nil {
			return 0, nil
		}
		str = strings.TrimSpace(str)
		if str == "" {
			return 0, nil
		}
		if i, err := strconv.ParseInt(str, 10, 64); err == nil {
			return i, nil
		}
		if f, err := strconv.ParseFloat(str, 64); err == nil {
			return int64(f), nil
		}
		return 0, nil
	}
	if i, err := strconv.ParseInt(string(data), 10, 64); err == nil {
		return i, nil
	}
	if f, err := strconv.ParseFloat(string(data), 64); err == nil {
		return int64(f), nil
	}
	return 0, nil
}

// FlexFloat64 unmarshals a JSON number, numeric string (`"3.5"`), or PHP `false`/`null` into a float64.
type FlexFloat64 float64

func (f *FlexFloat64) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || string(data) == "null" || string(data) == "false" {
		*f = 0
		return nil
	}
	if data[0] == '"' {
		var str string
		if err := json.Unmarshal(data, &str); err != nil {
			*f = 0
			return nil
		}
		val, _ := strconv.ParseFloat(strings.TrimSpace(str), 64)
		*f = FlexFloat64(val)
		return nil
	}
	val, _ := strconv.ParseFloat(string(data), 64)
	*f = FlexFloat64(val)
	return nil
}

// FlexBool unmarshals a JSON bool, `"true"`/`"false"`, `1`/`0`, or null into a bool.
type FlexBool bool

func (b *FlexBool) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	switch string(data) {
	case "true", "1", `"true"`, `"1"`, `"yes"`:
		*b = true
	default:
		*b = false
	}
	return nil
}

// FlexIntSlice unmarshals a JSON array of ints/numeric strings, or PHP `false`/`null`/`{}` into `[]int`.
type FlexIntSlice []int

func (s *FlexIntSlice) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || data[0] != '[' {
		*s = nil
		return nil
	}
	var raw []FlexInt
	if err := json.Unmarshal(data, &raw); err != nil {
		*s = nil
		return nil
	}
	out := make([]int, len(raw))
	for i, v := range raw {
		out[i] = int(v)
	}
	*s = out
	return nil
}

// FlexStringSlice unmarshals a JSON array of strings, or PHP `false`/`null`/`{}` into `[]string`.
type FlexStringSlice []string

func (s *FlexStringSlice) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || data[0] != '[' {
		*s = nil
		return nil
	}
	var raw []FlexString
	if err := json.Unmarshal(data, &raw); err != nil {
		*s = nil
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		if v != "" {
			out = append(out, string(v))
		}
	}
	*s = out
	return nil
}

// FlexMap unmarshals a JSON object `{...}` into `map[string]T`, while gracefully ignoring
// PHP's notorious `[]` (empty array) or `false` when an associative array is empty.
type FlexMap[T any] map[string]T

func (m *FlexMap[T]) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || data[0] != '{' {
		*m = nil
		return nil
	}
	// Unmarshal key by key so one corrupt entry in a PHP associative array doesn't discard valid siblings
	var rawEntries map[string]json.RawMessage
	if err := json.Unmarshal(data, &rawEntries); err != nil {
		*m = nil
		return nil
	}
	out := make(map[string]T, len(rawEntries))
	for k, rawVal := range rawEntries {
		var val T
		if err := json.Unmarshal(rawVal, &val); err == nil {
			out[k] = val
		}
	}
	*m = out
	return nil
}

// FlexSlice unmarshals a JSON array `[...]` or PHP numeric-keyed object `{"0": ..., "1": ...}`
// into `[]T`, decoding element-by-element so a single malformed item never aborts the entire page.
type FlexSlice[T any] []T

func (s *FlexSlice[T]) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || string(data) == "null" || string(data) == "false" {
		*s = nil
		return nil
	}
	if data[0] == '[' {
		var rawItems []json.RawMessage
		if err := json.Unmarshal(data, &rawItems); err != nil {
			*s = nil
			return nil
		}
		out := make([]T, 0, len(rawItems))
		for _, raw := range rawItems {
			var item T
			if err := json.Unmarshal(raw, &item); err == nil {
				out = append(out, item)
			}
		}
		*s = out
		return nil
	}
	if data[0] == '{' {
		var rawMap map[string]json.RawMessage
		if err := json.Unmarshal(data, &rawMap); err != nil {
			*s = nil
			return nil
		}
		out := make([]T, 0, len(rawMap))
		for _, raw := range rawMap {
			var item T
			if err := json.Unmarshal(raw, &item); err == nil {
				out = append(out, item)
			}
		}
		*s = out
		return nil
	}
	*s = nil
	return nil
}
