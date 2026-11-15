package launchd

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPlistIsValidAndEscaped(t *testing.T) {
	p := Plist("/Users/a & b/go/bin/tick", "/tmp/<log>")
	if !strings.Contains(p, "/Users/a &amp; b/go/bin/tick") || !strings.Contains(p, "/tmp/&lt;log&gt;") {
		t.Fatal("paths are not XML-escaped")
	}
	if _, err := exec.LookPath("plutil"); err != nil {
		t.Skip("plutil not available")
	}
	f := filepath.Join(t.TempDir(), "agent.plist")
	os.WriteFile(f, []byte(p), 0o644)
	if out, err := exec.Command("plutil", "-lint", f).CombinedOutput(); err != nil {
		t.Fatalf("plutil: %v %s", err, out)
	}
}
