package git

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/gitenv"
)

// initRepo creates a repository at dir using an environment that cannot be
// redirected by the fixture's own decoy variables.
func initRepo(t *testing.T, dir string) {
	t.Helper()
	cmd := exec.Command("git", "init", "--quiet")
	cmd.Dir = dir
	cmd.Env = append(gitenv.ScrubRouting(os.Environ()), "GIT_CONFIG_NOSYSTEM=1")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init in %s: %v\n%s", dir, err, output)
	}
}

// resolve canonicalizes a path so the comparison does not fail on a platform
// whose temp directory is reached through a symlink (macOS /var -> /private/var).
func resolve(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("EvalSymlinks(%s): %v", path, err)
	}
	return resolved
}

// TestRevParseGitDirsAnswersForItsArgument pins the boundary RevParseGitDirs
// exists to draw. GIT_DIR and GIT_COMMON_DIR outrank `git -C`, so an
// unscrubbed copy of this probe answers for whatever repository the
// environment names instead of the one the caller passed -- which is how the
// three startup probes that share it could resolve the config from one
// repository and the database from another.
func TestRevParseGitDirsAnswersForItsArgument(t *testing.T) {
	target, decoy := t.TempDir(), t.TempDir()
	initRepo(t, target)
	initRepo(t, decoy)

	decoyGitDir := filepath.Join(decoy, ".git")
	t.Setenv("GIT_DIR", decoyGitDir)
	t.Setenv("GIT_COMMON_DIR", decoyGitDir)

	// Precondition: the redirect really does outrank -C, so a green assertion
	// below cannot be green because the decoy was inert.
	unscrubbed := exec.Command("git", "-C", target, "rev-parse", "--git-dir")
	inherited, err := unscrubbed.Output()
	if err != nil {
		t.Fatalf("unscrubbed probe failed: %v", err)
	}
	if got := resolve(t, strings.TrimSpace(string(inherited))); got != resolve(t, decoyGitDir) {
		t.Fatalf("redirect precondition: unscrubbed probe answered %q, want the decoy %q", got, decoyGitDir)
	}

	gitDir, commonDir, err := RevParseGitDirs(target)
	if err != nil {
		t.Fatalf("RevParseGitDirs(%s): %v", target, err)
	}
	wantGitDir := resolve(t, filepath.Join(target, ".git"))
	for name, got := range map[string]string{"gitDir": gitDir, "commonDir": commonDir} {
		// Git may answer with a repository-relative path, which is the
		// documented contract; canonicalize it against the argument.
		if !filepath.IsAbs(got) {
			got = filepath.Join(target, got)
		}
		if resolve(t, got) != wantGitDir {
			t.Errorf("%s = %q, want %q: the probe answered for the inherited repository", name, got, wantGitDir)
		}
	}
}

// TestRevParseGitDirsHonorsInheritedConfigFence is the other half of the
// contract: scrubbing removes redirects only, so a caller that fenced this
// process off from the host's global configuration keeps that boundary here.
// A deliberately malformed global config is the observable -- Git names the
// file it could not parse, which it cannot do if GIT_CONFIG_GLOBAL never
// reached the subprocess.
func TestRevParseGitDirsHonorsInheritedConfigFence(t *testing.T) {
	target := t.TempDir()
	initRepo(t, target)

	fenceDir := t.TempDir()
	good := filepath.Join(fenceDir, "good.gitconfig")
	if err := os.WriteFile(good, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(fenceDir, "bad.gitconfig")
	if err := os.WriteFile(bad, []byte("[broken\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("GIT_CONFIG_GLOBAL", good)
	if _, _, err := RevParseGitDirs(target); err != nil {
		t.Fatalf("RevParseGitDirs with a well-formed inherited global config: %v", err)
	}

	t.Setenv("GIT_CONFIG_GLOBAL", bad)
	_, _, err := RevParseGitDirs(target)
	if err == nil {
		t.Fatal("RevParseGitDirs succeeded with a malformed inherited global config; the fence was scrubbed away")
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("RevParseGitDirs error = %v, want an *exec.ExitError", err)
	}
	if !strings.Contains(string(exitErr.Stderr), bad) {
		t.Fatalf("Git did not name the fenced global config in %q", exitErr.Stderr)
	}
}
