package routing

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/gitenv"
)

// TestRoleFromGitConfigIgnoresInheritedRedirects exercises the REAL
// gitCommandRunner. Every other test in this package replaces it with a stub,
// so nothing else here can observe the scrub on line 27 -- deleting it leaves
// this package, internal/storage/domain/git and the cmd/bd role suites green.
//
// That matters more here than anywhere else in the tree: this package is the
// routing authority, so a redirected GIT_DIR that outranks cmd.Dir decides
// whether bd writes as a maintainer or as a contributor.
func TestRoleFromGitConfigIgnoresInheritedRedirects(t *testing.T) {
	target, decoy := t.TempDir(), t.TempDir()
	for dir, role := range map[string]UserRole{target: Contributor, decoy: Maintainer} {
		runFixtureGit(t, dir, "init", "--quiet")
		runFixtureGit(t, dir, "config", "--local", "beads.role", string(role))
	}

	// Both repositories carry a LOCAL beads.role, which outranks any global or
	// system value, so the assertions below cannot be decided by the host.
	t.Setenv("GIT_DIR", filepath.Join(decoy, ".git"))
	t.Setenv("GIT_WORK_TREE", decoy)

	// Precondition: the redirect really does outrank cmd.Dir, so a green
	// assertion below cannot be green because the decoy was inert.
	unscrubbed := exec.Command("git", "config", "--get", "beads.role")
	unscrubbed.Dir = target
	inherited, err := unscrubbed.Output()
	if err != nil {
		t.Fatalf("unscrubbed probe failed: %v", err)
	}
	if got := UserRole(strings.TrimSpace(string(inherited))); got != Maintainer {
		t.Fatalf("redirect precondition: unscrubbed git answered %q, want the decoy's %q", got, Maintainer)
	}

	role, ok := roleFromGitConfig(target)
	if !ok {
		t.Fatal("roleFromGitConfig() found no role for a repository that has one")
	}
	if role != Contributor {
		t.Errorf("roleFromGitConfig(target) = %q, want %q: the authority read the inherited repository", role, Contributor)
	}
}

// TestRoleFromGitConfigHonorsInheritedConfigFence is the other half: the scrub
// removes redirects only, so a process fenced off from the host's global
// configuration stays fenced. Pointing GIT_CONFIG_GLOBAL at a file that
// supplies beads.role is the observable, because a repository with no local
// value is exactly where that fence still decides the answer.
func TestRoleFromGitConfigHonorsInheritedConfigFence(t *testing.T) {
	target := t.TempDir()
	runFixtureGit(t, target, "init", "--quiet")

	globalConfig := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(globalConfig, []byte("[beads]\n\trole = contributor\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", globalConfig)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")

	role, ok := roleFromGitConfig(target)
	if !ok {
		t.Fatal("roleFromGitConfig() dropped the inherited global config fence")
	}
	if role != Contributor {
		t.Errorf("roleFromGitConfig() = %q, want %q from the fenced global config", role, Contributor)
	}
}

func runFixtureGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(gitenv.ScrubRouting(os.Environ()), "GIT_CONFIG_NOSYSTEM=1")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fixture git %v in %s: %v\n%s", args, dir, err, output)
	}
}
