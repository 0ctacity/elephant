package app_test

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// debugPrintln matches Go's builtin println while excluding fmt.Fprintln and
// method calls such as buf.println.
var debugPrintln = regexp.MustCompile(`(^|[^.\w])println\(`)

// moduleRoot walks up from the test working directory to the directory that
// holds go.mod.
func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found above the test working directory")
		}
		dir = parent
	}
}

// TestNoProductionDebugPrintln rejects the builtin println in production code:
// it writes unstructured text to stderr outside the logging boundary and cannot
// be leveled, formatted, or silenced.
func TestNoProductionDebugPrintln(t *testing.T) {
	root := moduleRoot(t)
	var violations []string
	for _, top := range []string{"cmd", "internal"} {
		err := filepath.WalkDir(filepath.Join(root, top), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			src, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for i, line := range strings.Split(string(src), "\n") {
				code := line
				if idx := strings.Index(code, "//"); idx >= 0 {
					code = code[:idx]
				}
				if debugPrintln.MatchString(code) {
					violations = append(violations, fmt.Sprintf("%s:%d: %s", rel, i+1, strings.TrimSpace(line)))
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(violations) > 0 {
		t.Fatalf("production debug println found:\n%s", strings.Join(violations, "\n"))
	}
}
