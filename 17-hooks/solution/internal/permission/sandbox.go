package permission

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Sandbox restricts what a command can write and whether it can use the
// network. On macOS it uses Seatbelt (sandbox-exec), modeled on Codex's
// policy: deny by default, allow reads and process creation, allow writes
// only under Writable (except Protected subpaths), network only if enabled.
type Sandbox struct {
	Writable  []string
	Protected []string
	Network   bool
}

const sandboxExec = "/usr/bin/sandbox-exec"

func SandboxAvailable() bool {
	if runtime.GOOS != "darwin" {
		return false
	}
	_, err := os.Stat(sandboxExec)
	return err == nil
}

const baseProfile = `(version 1)
(deny default)
(allow process-exec)
(allow process-fork)
(allow signal (target same-sandbox))
(allow process-info* (target same-sandbox))
(allow file-read*)
(allow sysctl-read)
(allow mach-lookup)
(allow ipc-posix-shm)
(allow pseudo-tty)
(allow file-ioctl)
(allow file-write-data (literal "/dev/null") (literal "/dev/zero") (literal "/dev/tty") (regex #"^/dev/fd/"))
`

// Profile renders the Seatbelt policy and its -D parameters. Paths are passed
// as parameters, never spliced into the policy text.
func (s Sandbox) Profile() (string, []string, error) {
	var b strings.Builder
	b.WriteString(baseProfile)
	var params []string
	var protect []string
	for i, p := range s.Protected {
		rp, err := real(p)
		if err != nil {
			return "", nil, err
		}
		name := fmt.Sprintf("PROTECTED_%d", i)
		params = append(params, fmt.Sprintf("-D%s=%s", name, rp))
		protect = append(protect, fmt.Sprintf(" (require-not (subpath (param %q)))", name))
	}
	for i, w := range s.Writable {
		rw, err := real(w)
		if err != nil {
			return "", nil, err
		}
		name := fmt.Sprintf("WRITABLE_ROOT_%d", i)
		params = append(params, fmt.Sprintf("-D%s=%s", name, rw))
		fmt.Fprintf(&b, "(allow file-write* (require-all (subpath (param %q))%s))\n", name, strings.Join(protect, ""))
	}
	if s.Network {
		b.WriteString("(allow network*)\n")
	}
	return b.String(), params, nil
}

// Wrap returns argv run inside the sandbox.
func (s Sandbox) Wrap(argv []string) ([]string, error) {
	if !SandboxAvailable() {
		return nil, errors.New("sandbox not available on this platform")
	}
	profile, params, err := s.Profile()
	if err != nil {
		return nil, err
	}
	out := append([]string{sandboxExec, "-p", profile}, params...)
	return append(append(out, "--"), argv...), nil
}

// real resolves symlinks; Seatbelt matches real paths (/var is /private/var on macOS).
func real(p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	if r, err := filepath.EvalSymlinks(abs); err == nil {
		return r, nil
	}
	return abs, nil
}
