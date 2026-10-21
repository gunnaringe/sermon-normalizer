package web

import (
	"encoding/json"
	"io/fs"
	"regexp"
	"sort"
	"strings"
	"testing"
)

const jsString = `'(?:[^'\\]|\\.)*'|"(?:[^"\\]|\\.)*"`

var (
	reT          = regexp.MustCompile(`(?:^|[^\w.$])t\(\s*(` + jsString + `)`)
	reTn         = regexp.MustCompile(`(?:^|[^\w.$])tn\([^'"]*?,\s*(` + jsString + `)\s*,\s*(` + jsString + `)`)
	reAttr       = regexp.MustCompile(`data-i18n(?:-[\w-]+)?="([^"]*)"`)
	reLocaleLine = regexp.MustCompile(`(?m)^\s*("(?:[^"\\]|\\.)*"):\s*("(?:[^"\\]|\\.)*"),$`)
	reBraces     = regexp.MustCompile(`\{(\w+)\}`)
)

func unquoteJS(s string) string {
	body := s[1 : len(s)-1]
	return regexp.MustCompile(`\\(.)`).ReplaceAllStringFunc(body, func(m string) string {
		if m[1] == 'n' {
			return "\n"
		}
		return m[1:]
	})
}

// localeDict reads one of the dictionaries of locales.js ("nb" or "nn").
func localeDict(t *testing.T, src, name string) map[string]string {
	start := strings.Index(src, "export const "+name+" = {")
	if start < 0 {
		t.Fatalf("no %s in locales.js", name)
	}
	end := strings.Index(src[start:], "\n};")
	out := map[string]string{}
	for _, m := range reLocaleLine.FindAllStringSubmatch(src[start:start+end], -1) {
		var k, v string
		if json.Unmarshal([]byte(m[1]), &k) != nil || json.Unmarshal([]byte(m[2]), &v) != nil {
			t.Fatalf("locales.js: bad line %s", m[0])
		}
		out[k] = v
	}
	return out
}

func placeholderNamesOf(s string) []string {
	var out []string
	for _, m := range reBraces.FindAllStringSubmatch(s, -1) {
		out = append(out, m[1])
	}
	sort.Strings(out)
	return out
}

// Every text the interface shows through t() or tn() has a translation in
// both languages, with the same {placeholders}.
func TestLocales(t *testing.T) {
	b, err := static.ReadFile("static/locales.js")
	if err != nil {
		t.Fatal(err)
	}
	dicts := map[string]map[string]string{"nb": localeDict(t, string(b), "nb"), "nn": localeDict(t, string(b), "nn")}
	if len(dicts["nb"]) < 100 || len(dicts["nn"]) != len(dicts["nb"]) {
		t.Fatalf("locales.js has %d (nb) and %d (nn) texts", len(dicts["nb"]), len(dicts["nn"]))
	}

	used := map[string]string{} // text → where
	// Words chosen in the code by name, not written in a t() call.
	for _, k := range []string{"paused", "expired", "used up", "full", "left", "right"} {
		used[k] = "shares.js and editor.js (by name)"
	}
	err = fs.WalkDir(static, "static", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || strings.Contains(path, "/vendor/") || strings.HasSuffix(path, "/locales.js") || strings.HasSuffix(path, "/i18n.js") {
			return err
		}
		src, err := static.ReadFile(path)
		if err != nil {
			return err
		}
		switch {
		case strings.HasSuffix(path, ".js"):
			for _, m := range reT.FindAllStringSubmatch(string(src), -1) {
				used[unquoteJS(m[1])] = path
			}
			for _, m := range reTn.FindAllStringSubmatch(string(src), -1) {
				used[unquoteJS(m[1])] = path
				used[unquoteJS(m[2])] = path
			}
		case strings.HasSuffix(path, ".html"):
			for _, m := range reAttr.FindAllStringSubmatch(string(src), -1) {
				used[m[1]] = path
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(used) < 300 {
		t.Fatalf("found only %d texts in the code: has the way they are written changed?", len(used))
	}
	for text, where := range used {
		for lang, dict := range dicts {
			got, ok := dict[text]
			switch {
			case !ok:
				t.Errorf("%s has no translation of %q (used in %s)", lang, text, where)
			case strings.Join(placeholderNamesOf(got), ",") != strings.Join(placeholderNamesOf(text), ","):
				t.Errorf("%s: %q has other {placeholders} than %q", lang, got, text)
			case strings.TrimSpace(got) == "":
				t.Errorf("%s: %q is empty", lang, text)
			}
		}
	}
	for text := range dicts["nb"] {
		if _, ok := dicts["nn"][text]; !ok {
			t.Errorf("nn lacks %q, which nb has", text)
		}
	}
}
