package permission_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"harness/internal/permission"
)

func sandboxed(t *testing.T, sb permission.Sandbox, dir, script string) (string, error) {
	t.Helper()
	argv, err := sb.Wrap([]string{"/bin/bash", "-c", script})
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestSandboxWrites(t *testing.T) {
	if !permission.SandboxAvailable() {
		t.Skip("Seatbelt is macOS-only")
	}
	root, outside := t.TempDir(), t.TempDir()
	os.Mkdir(filepath.Join(root, ".git"), 0o755)
	sb := permission.Sandbox{Writable: []string{root}, Protected: []string{filepath.Join(root, ".git")}}

	if out, err := sandboxed(t, sb, root, "echo hi > inside.txt && mkdir -p a/b && touch a/b/c"); err != nil {
		t.Fatalf("writes inside the workspace must work: %v %s", err, out)
	}
	out, err := sandboxed(t, sb, root, "touch "+filepath.Join(outside, "escape.txt"))
	if err == nil || !strings.Contains(out, "Operation not permitted") {
		t.Errorf("write outside the workspace: err=%v out=%q", err, out)
	}
	if _, err := os.Stat(filepath.Join(outside, "escape.txt")); err == nil {
		t.Error("file created outside the workspace")
	}
	if out, err := sandboxed(t, sb, root, "touch .git/hooks-pre-commit"); err == nil {
		t.Errorf(".git must stay read-only inside a writable root: %s", out)
	}
	if out, err := sandboxed(t, sb, root, "cat /etc/hosts >/dev/null && echo read-ok"); err != nil || !strings.Contains(out, "read-ok") {
		t.Errorf("reads should be allowed: %v %s", err, out)
	}
}

func TestSandboxNetwork(t *testing.T) {
	if !permission.SandboxAvailable() {
		t.Skip("Seatbelt is macOS-only")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("pong")) }))
	defer srv.Close()
	root := t.TempDir()
	off := permission.Sandbox{Writable: []string{root}}
	if out, err := sandboxed(t, off, root, "curl -s -m 3 "+srv.URL); err == nil || strings.Contains(out, "pong") {
		t.Errorf("network must be blocked by default: err=%v out=%q", err, out)
	}
	on := permission.Sandbox{Writable: []string{root}, Network: true}
	if out, err := sandboxed(t, on, root, "curl -s -m 3 "+srv.URL); err != nil || !strings.Contains(out, "pong") {
		t.Errorf("Network: true should allow it: err=%v out=%q", err, out)
	}
}

func TestSandboxProfileUsesParams(t *testing.T) {
	sb := permission.Sandbox{Writable: []string{"/tmp/has \"quote"}}
	profile, params, err := sb.Profile()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(profile, "has") || len(params) != 1 || !strings.HasPrefix(params[0], "-DWRITABLE_ROOT_0=") {
		t.Errorf("paths must be passed as -D parameters, not spliced into the policy:\n%s\n%v", profile, params)
	}
}
