package gm

import (
	"bytes"
	"encoding/json"
	"io"
)

// redactedKeys are log fields libgm may fill with raw protocol data or
// response bodies. They are removed before anything reaches the log file.
var redactedKeys = []string{"data", "resp_body", "body", "devices", "content", "text", "message_data", "cookies", "token"}

// redactingWriter strips risky fields from libgm's JSON log lines.
type redactingWriter struct{ w io.Writer }

func (r redactingWriter) Write(p []byte) (int, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(bytes.TrimSpace(p), &obj); err != nil {
		// Not a JSON object: drop it rather than risk writing payload data.
		return len(p), nil
	}
	changed := false
	for _, k := range redactedKeys {
		if _, ok := obj[k]; ok {
			obj[k] = json.RawMessage(`"[redacted]"`)
			changed = true
		}
	}
	if !changed {
		_, err := r.w.Write(p)
		return len(p), err
	}
	out, err := json.Marshal(obj)
	if err != nil {
		return len(p), nil
	}
	_, err = r.w.Write(append(out, '\n'))
	return len(p), err
}
