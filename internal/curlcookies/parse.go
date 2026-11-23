// Package curlcookies extracts the Google cookies that pairing needs from
// whatever the user copied: a "Copy as cURL" command (bash or Windows cmd
// style, from Chrome or Firefox), a JSON object, a cookie-editor JSON export,
// or a raw "Cookie:" header.
package curlcookies

import (
	"encoding/json"
	"errors"
	"regexp"
	"sort"
	"strings"
)

// Required cookies, per mautrix-gmessages' Google account login.
var Required = []string{"SID", "HSID", "SSID", "OSID", "APISID", "SAPISID"}

// Optional cookies that help when present.
var Optional = []string{"__Secure-1PSIDTS"}

// ErrMissing is returned when required cookies are absent.
type ErrMissing struct{ Names []string }

func (e ErrMissing) Error() string {
	return "these required cookies were not found: " + strings.Join(e.Names, ", ") +
		". Make sure you copied the /web/config request, signed in to only one Google account, and used a private window."
}

var cmdEscape = regexp.MustCompile(`\^(.)`)

// Parse returns the cookies that were found. It never logs or prints values.
func Parse(input string) (map[string]string, error) {
	text := strings.TrimSpace(strings.TrimPrefix(input, string(rune(0xFEFF))))
	if text == "" {
		return nil, errors.New("nothing was copied")
	}
	wanted := append(append([]string{}, Required...), Optional...)
	found := map[string]string{}

	if strings.HasPrefix(text, "{") || strings.HasPrefix(text, "[") {
		parseJSON(text, wanted, found)
	}
	if len(found) == 0 {
		// Windows cmd style escapes quotes and other characters with ^.
		if strings.Contains(text, `^"`) {
			text = strings.ReplaceAll(text, "^\r\n", " ")
			text = strings.ReplaceAll(text, "^\n", " ")
			text = cmdEscape.ReplaceAllString(text, "$1")
		}
		for _, name := range wanted {
			re := regexp.MustCompile(`(?:^|[\s;'"])` + regexp.QuoteMeta(name) + `=([^;\s'"]+)`)
			if m := re.FindStringSubmatch(text); m != nil {
				found[name] = m[1]
			}
		}
	}

	var missing []string
	for _, name := range Required {
		if found[name] == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return found, ErrMissing{Names: missing}
	}
	return found, nil
}

func parseJSON(text string, wanted []string, found map[string]string) {
	var obj map[string]any
	if err := json.Unmarshal([]byte(text), &obj); err == nil {
		for _, name := range wanted {
			if v, ok := obj[name].(string); ok && v != "" {
				found[name] = v
			}
		}
		return
	}
	// Cookie-editor style: [{"name": "SID", "value": "...", "domain": ".google.com"}, ...]
	var list []struct {
		Name   string `json:"name"`
		Value  string `json:"value"`
		Domain string `json:"domain"`
	}
	if err := json.Unmarshal([]byte(text), &list); err == nil {
		for _, c := range list {
			for _, name := range wanted {
				if c.Name == name && c.Value != "" && found[name] == "" {
					found[name] = c.Value
				}
			}
		}
	}
}

// Names returns the cookie names in m, sorted, for display without values.
func Names(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
