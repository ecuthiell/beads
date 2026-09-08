package scripts_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestUpgradeSmokeMultiVersionDispatch(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, candidate, failVersion string
		wantExit                     int
	}{
		{"prebuilt", "candidate with spaces/bd", "", 0},
		{"automatic", "", "", 0},
		{"continues after failure", "candidate with spaces/bd", "v0.61.0", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			const candidateHelper = "scripts/lib/smoke-candidate.sh"
			for _, path := range []string{"scripts/upgrade-smoke-test.sh", ".buildflags", candidateHelper} {
				data, err := os.ReadFile(filepath.Join(sourceRepoRoot(t), path))
				if err != nil {
					if path == candidateHelper && os.IsNotExist(err) {
						continue
					}
					t.Fatal(err)
				}
				destination := filepath.Join(dir, path)
				if err := os.MkdirAll(filepath.Dir(destination), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(destination, data, 0755); err != nil {
					t.Fatal(err)
				}
			}
			// Exit at child startup, before any recursion, download, build or DB work.
			const observer = `if [ -z "${BEADS_LOOP_ROOT:-}" ]; then
    export BEADS_LOOP_ROOT=1
else
    printf '%s\t%s\t%s\n' "${1:-}" "${CANDIDATE_BIN:-}" "${SMOKE_VERSIONS:-}" >> calls
    [ -z "${SMOKE_VERSIONS:-}" ] || exit 42
    [ "${1:-}" != "$BEADS_LOOP_FAIL" ] || exit 19
    exit 0
fi
`
			if err := os.WriteFile(filepath.Join(dir, "observe-child.sh"), []byte(observer), 0600); err != nil {
				t.Fatal(err)
			}
			path := os.Getenv("PATH")
			if runtime.GOOS == "windows" {
				path = "/usr/bin:/bin"
			}
			// Relative fixture paths use the same mount under both Bash invocations.
			env := []string{"PATH=" + path, "LC_ALL=C", "LANG=C", "ENV=", "BASH_ENV=./observe-child.sh",
				"BEADS_LOOP_FAIL=" + tc.failVersion, "CANDIDATE_BIN=" + tc.candidate,
				"SMOKE_VERSIONS=v0.62.0 v0.61.0 v0.60.0"}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, bash, "--noprofile", "--norc", "scripts/upgrade-smoke-test.sh")
			cmd.Dir, cmd.Env = dir, env
			out, err := cmd.CombinedOutput()
			if ctx.Err() != nil || cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != tc.wantExit {
				t.Fatalf("dispatch exit = %v, want %d: %s", err, tc.wantExit, out)
			}
			got, err := os.ReadFile(filepath.Join(dir, "calls"))
			if err != nil {
				t.Fatal(err)
			}
			var want strings.Builder
			for _, version := range []string{"v0.62.0", "v0.61.0", "v0.60.0"} {
				want.WriteString(version + "\t" + tc.candidate + "\t\n")
			}
			if string(got) != want.String() {
				t.Fatalf("child dispatch = %q, want %q", got, want.String())
			}
		})
	}
}
