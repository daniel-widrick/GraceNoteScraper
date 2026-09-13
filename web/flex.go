package web

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
)

// FlexString decodes a JSON string, number, or null into a string. Gracenote
// is inconsistent about scalar encodings, and a decode error on one optional
// field would otherwise discard an entire six-hour grid.
type FlexString string

func (f *FlexString) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || bytes.Equal(data, []byte("null")) {
		*f = ""
		return nil
	}
	if data[0] == '"' {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		*f = FlexString(s)
		return nil
	}
	var n json.Number
	if err := json.Unmarshal(data, &n); err == nil {
		*f = FlexString(n.String())
		return nil
	}
	var b bool
	if err := json.Unmarshal(data, &b); err == nil {
		*f = FlexString(strconv.FormatBool(b))
		return nil
	}
	*f = ""
	return nil
}

// FlexBool decodes a JSON bool, a 0/1 number, a "0"/"1"/"true"/"false" string,
// or null into a bool. Anything unrecognized decodes as false rather than
// failing the surrounding document.
type FlexBool bool

func (f *FlexBool) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || bytes.Equal(data, []byte("null")) {
		*f = false
		return nil
	}
	var b bool
	if err := json.Unmarshal(data, &b); err == nil {
		*f = FlexBool(b)
		return nil
	}
	var n json.Number
	if err := json.Unmarshal(data, &n); err == nil {
		*f = FlexBool(n.String() != "0" && n.String() != "0.0")
		return nil
	}
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		switch strings.ToLower(strings.TrimSpace(s)) {
		case "1", "true", "yes":
			*f = true
		default:
			*f = false
		}
		return nil
	}
	*f = false
	return nil
}
