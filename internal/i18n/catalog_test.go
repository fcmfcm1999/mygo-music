package i18n

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// goFiles parses each non-test Go file of the app's packages, but this one.
func goFiles(t *testing.T, visit func(path string, f *ast.File, fset *token.FileSet)) {
	t.Helper()
	for _, dir := range []string{filepath.Join("..", "..", "internal")} {
		err := filepath.WalkDir(dir, func(path string, e fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if e.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			fset := token.NewFileSet()
			f, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				return err
			}
			visit(path, f, fset)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

// keyOf returns the text of a call that marks text for translation, as T("...")
// or i18n.Tf("...", n), and TC("...", "context") as the catalog has it.
func keyOf(call *ast.CallExpr) (string, bool) {
	var name string
	switch fn := call.Fun.(type) {
	case *ast.Ident:
		name = fn.Name
	case *ast.SelectorExpr:
		if pkg, ok := fn.X.(*ast.Ident); !ok || pkg.Name != "i18n" {
			return "", false
		}
		name = fn.Sel.Name
	default:
		return "", false
	}
	if name != "T" && name != "Tf" && name != "N" && name != "TC" || len(call.Args) == 0 {
		return "", false
	}
	text, ok := literal(call.Args[0])
	if !ok || name != "TC" {
		return text, ok
	}
	if len(call.Args) < 2 {
		return "", false
	}
	context, ok := literal(call.Args[1])
	return text + "|" + context, ok
}

func literal(e ast.Expr) (string, bool) {
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	text, err := strconv.Unquote(lit.Value)
	return text, err == nil
}

// Every text the code translates has its Simplified Chinese, or a new string
// would show in English among the Chinese. A text with verbs keeps them.
func TestCatalogHasEveryKey(t *testing.T) {
	verbs := regexp.MustCompile(`%(\[\d+\])?[-+# 0-9.]*[a-zA-Z]`)
	norm := func(s string) string {
		var out []string
		for _, v := range verbs.FindAllString(s, -1) {
			out = append(out, regexp.MustCompile(`\[\d+\]`).ReplaceAllString(v, ""))
		}
		return strings.Join(out, " ")
	}
	seen := 0
	goFiles(t, func(path string, f *ast.File, fset *token.FileSet) {
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			key, ok := keyOf(call)
			if !ok {
				return true
			}
			seen++
			zh, ok := zhCN[key]
			if !ok {
				t.Errorf("%s: no translation of %q", fset.Position(call.Pos()), key)
				return true
			}
			if norm(key) != norm(zh) {
				t.Errorf("%s: the translation of %q has other verbs: %q", fset.Position(call.Pos()), key, zh)
			}
			return true
		})
	})
	if seen < 100 {
		t.Errorf("found only %d texts: the scan has gone wrong", seen)
	}
}

// No Chinese is left in the code outside the catalogs and what reads lyrics,
// where it's data: a string written in place shows whatever the language.
func TestNoChineseInCode(t *testing.T) {
	han := regexp.MustCompile(`\p{Han}`)
	goFiles(t, func(path string, f *ast.File, fset *token.FileSet) {
		if strings.Contains(path, filepath.Join("internal", "i18n")) || strings.Contains(path, filepath.Join("internal", "lyrics")) {
			return
		}
		ast.Inspect(f, func(n ast.Node) bool {
			if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING && han.MatchString(lit.Value) {
				t.Errorf("%s: %s", fset.Position(lit.Pos()), lit.Value)
			}
			return true
		})
	})
}

// A language the app doesn't have is English, and switching applies at once.
func TestSwitchingLanguage(t *testing.T) {
	defer SetLanguage(English)
	SetLanguage(Chinese)
	if got := T("1 song"); got != "1 首" {
		t.Errorf("Chinese: %q", got)
	}
	SetLanguage("fr")
	if got := T("1 song"); got != "1 song" || Language() != English {
		t.Errorf("another language: %q, %s", got, Language())
	}
	if got := T("no such text"); got != "no such text" {
		t.Errorf("a text without a translation: %q", got)
	}
}

// A text in a context has its own translation, and reads as itself in English:
// keeping a playlist's new name isn't adding it to the library.
func TestTextInContext(t *testing.T) {
	defer SetLanguage(English)
	SetLanguage(Chinese)
	if got := T("Save"); got != "保存" {
		t.Errorf("Save: %q", got)
	}
	if got := TC("Save", "library"); got != "收藏" {
		t.Errorf("Save to the library: %q", got)
	}
	SetLanguage(English)
	if got := TC("Save", "library"); got != "Save" {
		t.Errorf("English: %q", got)
	}
}

// How long ago reads by the size of the gap, in the language.
func TestAgo(t *testing.T) {
	defer SetLanguage(English)
	now := time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		ago    time.Duration
		en, zh string
	}{
		{30 * time.Second, "just now", "刚刚"},
		{5 * time.Minute, "5 minutes ago", "5 分钟前"},
		{3 * time.Hour, "3 hours ago", "3 小时前"},
		{30 * time.Hour, "yesterday", "昨天"},
		{5 * 24 * time.Hour, "5 days ago", "5 天前"},
		{40 * 24 * time.Hour, "February 3", "2月3日"},
	} {
		SetLanguage(English)
		if got := Ago(now.Add(-c.ago), now); got != c.en {
			t.Errorf("%v ago: %q, want %q", c.ago, got, c.en)
		}
		SetLanguage(Chinese)
		if got := Ago(now.Add(-c.ago), now); got != c.zh {
			t.Errorf("%v ago: %q, want %q", c.ago, got, c.zh)
		}
	}
}

// Counts read shortly, by how people count in the language.
func TestCount(t *testing.T) {
	defer SetLanguage(English)
	for _, c := range []struct {
		n      int
		en, zh string
	}{{999, "999", "999"}, {12345, "12.3K", "1.2万"}, {2500000, "2.5M", "250.0万"}, {300000000, "300.0M", "3.0亿"}} {
		SetLanguage(English)
		if got := Count(c.n); got != c.en {
			t.Errorf("%d: %q", c.n, got)
		}
		SetLanguage(Chinese)
		if got := Count(c.n); got != c.zh {
			t.Errorf("%d: %q", c.n, got)
		}
	}
}

// Following the system picks the first language it prefers that the app has, in
// whatever form it's named, and a language chosen wins over it.
func TestResolve(t *testing.T) {
	for _, c := range []struct {
		setting   string
		preferred []string
		want      string
	}{
		{System, []string{"zh-Hans-CN", "en-GB"}, Chinese},
		{System, []string{"zh_TW"}, Chinese},
		{System, []string{"en-GB", "zh-Hans"}, English},
		{System, []string{"fr-FR", "zh-Hans"}, Chinese},
		{System, []string{"fr-FR"}, English},
		{System, nil, English},
		{"", []string{"zh-Hans"}, Chinese},
		{English, []string{"zh-Hans"}, English},
		{Chinese, []string{"en-US"}, Chinese},
	} {
		if got := Resolve(c.setting, c.preferred); got != c.want {
			t.Errorf("Resolve(%q, %v) = %q, want %q", c.setting, c.preferred, got, c.want)
		}
	}
}
