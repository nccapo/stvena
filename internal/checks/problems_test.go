package checks

import (
	"path/filepath"
	"testing"
)

func TestParseProblemsNormalizesSupportedLocations(t *testing.T) {
	work := t.TempDir()
	output := "\x1b[31msrc/app.go:12:4: undefined: user\x1b[0m\n" +
		"    app_test.go:24: got 2, want 1\n" +
		"src/ui.ts(8,3): error TS2322: wrong type\n" +
		"--> src/lib.rs:42:5\n" +
		"  File \"" + filepath.Join(work, "python app.py") + "\", line 9, in run\n" +
		"../../outside.go:1: bad\n/etc/passwd:2: outside\nordinary log output\n"
	p := ParseProblems(output, work)
	if len(p) != 5 {
		t.Fatalf("unexpected locations: %+v", p)
	}
	if p[0].Path != "src/app.go" || p[0].Line != 12 || p[0].Column != 4 {
		t.Fatalf("Go location: %+v", p[0])
	}
	if p[2].Line != 8 || p[2].Column != 3 {
		t.Fatalf("tsc location: %+v", p[2])
	}
	if p[4].Path != "python app.py" || p[4].Line != 9 {
		t.Fatalf("checkout normalization: %+v", p[4])
	}
}
