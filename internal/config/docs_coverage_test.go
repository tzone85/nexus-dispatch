package config_test

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/config"
)

// TestDocs_ConfigurationGuideCoversEveryKey walks config.Config through its
// `yaml` struct tags and asserts that every leaf key path (for example
// `security.gate_severity`) is documented in docs/guides/configuration.md,
// either as an inline code span (`security.gate_severity`) or as a key line
// inside a ```yaml block whose enclosing keys spell the same path.
//
// Map-valued fields whose values are structs (runtimes, plugins.providers,
// billing.llm_costs.rates) produce a wildcard segment: `runtimes.*.command`
// is satisfied by a yaml block such as
//
//	runtimes:
//	  gemma:
//	    command: …
//
// Slices of structs produce a `[]` suffix: `qa.success_criteria[].kind`.
//
// A key may be omitted from the guide only when it is listed in
// undocumentedAllowlist with a reason.
func TestDocs_ConfigurationGuideCoversEveryKey(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Join(filepath.Dir(thisFile), "..", "..")
	docPath := filepath.Join(root, "docs", "guides", "configuration.md")
	raw, err := os.ReadFile(docPath)
	if err != nil {
		t.Fatalf("read %s: %v", docPath, err)
	}
	doc := string(raw)

	want := leafKeyPaths(reflect.TypeOf(config.Config{}), "")
	if len(want) < 100 {
		t.Fatalf("reflection found only %d leaf keys — the walker is broken", len(want))
	}

	codeSpans := codeSpanSet(doc)
	yamlPaths := yamlKeyPathSet(doc)

	var missing []string
	for _, key := range want {
		if reason, ok := undocumentedAllowlist[key]; ok {
			if reason == "" {
				t.Errorf("allowlist entry %q has no reason", key)
			}
			continue
		}
		if codeSpans[key] || matchesAny(key, codeSpans) || matchesAny(key, yamlPaths) {
			continue
		}
		missing = append(missing, key)
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Errorf("%d config keys are not documented in docs/guides/configuration.md:\n  %s",
			len(missing), strings.Join(missing, "\n  "))
	}

	// The allowlist must not go stale: every entry has to name a real key.
	known := map[string]bool{}
	for _, k := range want {
		known[k] = true
	}
	for k := range undocumentedAllowlist {
		if !known[k] {
			t.Errorf("undocumentedAllowlist names %q, which is not a config key any more — remove it", k)
		}
	}
}

// undocumentedAllowlist lists leaf keys the guide may legitimately omit.
// Every entry needs a reason; an entry naming a key that no longer exists
// fails the test so the list cannot rot.
var undocumentedAllowlist = map[string]string{
	// Plugin manifests are authored by plugin developers, not operators; the
	// per-item schema (name/file/inject_when/roles, name/file/after,
	// command/models) is documented by the plugin loader's own docs, while
	// the guide only names the four plugins.* sections.
	"plugins.playbooks[].name":        "plugin manifest schema, not an operator knob",
	"plugins.playbooks[].file":        "plugin manifest schema, not an operator knob",
	"plugins.playbooks[].inject_when": "plugin manifest schema, not an operator knob",
	"plugins.playbooks[].roles":       "plugin manifest schema, not an operator knob",
	"plugins.qa[].name":               "plugin manifest schema, not an operator knob",
	"plugins.qa[].file":               "plugin manifest schema, not an operator knob",
	"plugins.qa[].after":              "plugin manifest schema, not an operator knob",
	"plugins.providers.*.command":     "plugin manifest schema, not an operator knob",
	"plugins.providers.*.models":      "plugin manifest schema, not an operator knob",
}

// leafKeyPaths returns the dotted yaml key path of every leaf field reachable
// from t, in declaration order. Structs recurse; maps of structs add a "*"
// segment; slices of structs add "[]" to the parent segment; everything
// else is a leaf.
func leafKeyPaths(t reflect.Type, prefix string) []string {
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	var out []string
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.PkgPath != "" { // unexported
			continue
		}
		tag := f.Tag.Get("yaml")
		if tag == "" || tag == "-" {
			continue
		}
		name := strings.Split(tag, ",")[0]
		if name == "" {
			continue
		}
		path := name
		if prefix != "" {
			path = prefix + "." + name
		}
		out = append(out, leafFor(f.Type, path)...)
	}
	return out
}

func leafFor(ft reflect.Type, path string) []string {
	for ft.Kind() == reflect.Ptr {
		ft = ft.Elem()
	}
	switch ft.Kind() {
	case reflect.Struct:
		return leafKeyPaths(ft, path)
	case reflect.Map:
		if elem := deref(ft.Elem()); elem.Kind() == reflect.Struct {
			return leafKeyPaths(elem, path+".*")
		}
	case reflect.Slice:
		if elem := deref(ft.Elem()); elem.Kind() == reflect.Struct {
			return leafKeyPaths(elem, path+"[]")
		}
	}
	return []string{path}
}

func deref(t reflect.Type) reflect.Type {
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	return t
}

var codeSpanRe = regexp.MustCompile("`([^`\n]+)`")

// codeSpanSet collects every inline code span in doc (fenced blocks excluded).
func codeSpanSet(doc string) map[string]bool {
	out := map[string]bool{}
	inFence := false
	for _, line := range strings.Split(doc, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		for _, m := range codeSpanRe.FindAllStringSubmatch(line, -1) {
			out[strings.TrimSpace(m[1])] = true
		}
	}
	return out
}

var yamlKeyRe = regexp.MustCompile(`^(\s*)(- )?([A-Za-z_][A-Za-z0-9_-]*)\s*:`)

// yamlKeyPathSet walks every ```yaml fence in doc and returns the dotted path
// of each key line, derived from indentation. A `- key:` list item marks its
// parent as a list (`parent[]`). Flow-style maps such as
// `tech_lead: { provider: ollama, model: x }` contribute their inner keys too.
func yamlKeyPathSet(doc string) map[string]bool {
	out := map[string]bool{}
	type frame struct {
		indent int
		path   string
	}
	var stack []frame
	inYAML := false
	for _, raw := range strings.Split(doc, "\n") {
		trimmed := strings.TrimSpace(raw)
		if strings.HasPrefix(trimmed, "```") {
			inYAML = strings.HasPrefix(trimmed, "```yaml") || strings.HasPrefix(trimmed, "```yml")
			stack = nil
			continue
		}
		if !inYAML || trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		m := yamlKeyRe.FindStringSubmatch(raw)
		if m == nil {
			continue
		}
		indent := len(m[1])
		isItem := m[2] != ""
		key := m[3]
		for len(stack) > 0 && stack[len(stack)-1].indent >= indent {
			stack = stack[:len(stack)-1]
		}
		parent := ""
		if len(stack) > 0 {
			parent = stack[len(stack)-1].path
		}
		if isItem {
			// `- kind: x` opens a list item under parent. Push a synthetic
			// frame for the item (at the dash's column) so the item's other
			// keys, which sit two columns further in, resolve to parent[].key.
			if parent != "" {
				parent += "[]"
			}
			stack = append(stack, frame{indent: indent, path: parent})
			indent += 2
		}
		path := key
		if parent != "" {
			path = parent + "." + key
		}
		out[path] = true
		stack = append(stack, frame{indent: indent, path: path})

		// flow mapping on the same line: `key: { a: 1, b: 2 }`
		rest := raw[strings.Index(raw, key+":")+len(key)+1:]
		rest = strings.TrimSpace(rest)
		if strings.HasPrefix(rest, "{") {
			for _, part := range strings.Split(strings.Trim(rest, "{} "), ",") {
				kv := strings.SplitN(part, ":", 2)
				if len(kv) == 2 {
					out[path+"."+strings.TrimSpace(kv[0])] = true
				}
			}
		}
	}
	return out
}

// matchesAny reports whether want equals any documented path segment-for-
// segment. A "*" segment on EITHER side matches anything: the reflected key
// `runtimes.*.command` is satisfied by a yaml block naming `runtimes.gemma.
// command`, and the reflected key `models.junior.num_ctx` is satisfied by the
// guide's `models.*.num_ctx` code span (the guide explains that `*` is any
// role) — which keeps the reference readable instead of 8 × 6 near-identical
// rows.
func matchesAny(want string, have map[string]bool) bool {
	if have[want] {
		return true
	}
	ws := strings.Split(want, ".")
outer:
	for h := range have {
		hs := strings.Split(h, ".")
		if len(hs) != len(ws) {
			continue
		}
		for i := range ws {
			if ws[i] != "*" && hs[i] != "*" && ws[i] != hs[i] {
				continue outer
			}
		}
		return true
	}
	return false
}
