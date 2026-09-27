package config

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/gitenv"
)

func TestGetIdentityIgnoresInheritedGitRouting(t *testing.T) {
	ResetForTesting()
	t.Cleanup(ResetForTesting)
	t.Setenv("BEADS_IDENTITY", "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	if err := os.WriteFile(filepath.Join(home, ".gitconfig"), []byte("[user]\n\tname = home-user\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	target, decoy := t.TempDir(), t.TempDir()
	for _, repo := range []string{target, decoy} {
		runConfigProbeGit(t, repo, "init", "--quiet")
	}
	runConfigProbeGit(t, target, "config", "--local", "user.name", "target-user")
	runConfigProbeGit(t, decoy, "config", "--local", "user.name", "decoy-user")
	t.Chdir(target)
	if err := Initialize(); err != nil {
		t.Fatal(err)
	}
	Set("identity", "")
	fenceConfig := filepath.Join(t.TempDir(), "fenced.gitconfig")
	if err := os.WriteFile(fenceConfig, []byte("[user]\n\tname = fenced-user\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name       string
		env        map[string]string
		clearLocal bool
		want       string
	}{
		{name: "repository", env: map[string]string{"GIT_DIR": filepath.Join(decoy, ".git")}, want: "target-user"},
		{name: "inline config", env: map[string]string{"GIT_CONFIG_COUNT": "1", "GIT_CONFIG_KEY_0": "user.name", "GIT_CONFIG_VALUE_0": "injected-user"}, want: "target-user"},
		// GIT_CONFIG_GLOBAL is a FENCE, not a redirect: it is how a caller
		// isolates a command from the host's ~/.gitconfig. Scrubbing it would
		// fall back to "home-user" here, which is the host identity the caller
		// deliberately replaced -- so the fenced file must win. This case is
		// the regression pin for that direction; before the redirect/fence
		// split it asserted "home-user" and so pinned the defect.
		{name: "global config fence is honored", env: map[string]string{"GIT_CONFIG_GLOBAL": fenceConfig}, clearLocal: true, want: "fenced-user"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.clearLocal {
				runConfigProbeGit(t, target, "config", "--local", "--unset", "user.name")
			}
			for key, value := range tc.env {
				t.Setenv(key, value)
			}
			if got := GetIdentity(""); got != tc.want {
				t.Errorf("GetIdentity() = %q, want %q from the intended config", got, tc.want)
			}
			Set("identity", "configured-user")
			t.Cleanup(func() { Set("identity", "") })
			if got := GetIdentity("flag-user"); got != "flag-user" {
				t.Errorf("flag identity = %q", got)
			}
			if got := GetIdentity(""); got != "configured-user" {
				t.Errorf("configured identity = %q", got)
			}
		})
	}
	t.Run("missing Git retains hostname fallback", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())
		want, err := os.Hostname()
		if err != nil || want == "" {
			t.Fatalf("hostname fixture: %q, %v", want, err)
		}
		if got := GetIdentity(""); got != want {
			t.Errorf("hostname identity = %q, want %q", got, want)
		}
	})
}

func TestSecretGitTrackingIgnoresInheritedGitRouting(t *testing.T) {
	target, decoy := t.TempDir(), t.TempDir()
	for _, repo := range []string{target, decoy} {
		runConfigProbeGit(t, repo, "init", "--quiet")
	}
	tracked := filepath.Join(target, "config.yaml")
	untracked := filepath.Join(target, "untracked.yaml")
	for _, path := range []string{tracked, untracked} {
		if err := os.WriteFile(path, []byte("json: false\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	runConfigProbeGit(t, target, "add", "--", tracked)
	for _, tc := range []struct {
		name string
		env  map[string]string
	}{
		{name: "repository", env: map[string]string{"GIT_DIR": filepath.Join(decoy, ".git")}},
		{name: "index", env: map[string]string{"GIT_INDEX_FILE": filepath.Join(decoy, "missing-index")}},
		{name: "config", env: map[string]string{"GIT_CONFIG_COUNT": "1", "GIT_CONFIG_KEY_0": "core.worktree", "GIT_CONFIG_VALUE_0": decoy}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for key, value := range tc.env {
				t.Setenv(key, value)
			}
			if !isGitTracked(tracked) {
				t.Error("routing hid the tracked file in the intended repository")
			}
			if isGitTracked(untracked) {
				t.Error("untracked file reported as tracked")
			}
			if err := checkSecretGitTracked(tracked, "linear.api_key"); err == nil || !strings.Contains(err.Error(), "refusing to write secret key") {
				t.Errorf("tracked secret refusal = %v", err)
			}
			if err := checkSecretGitTracked(untracked, "linear.api_key"); err != nil {
				t.Errorf("untracked secret refused: %v", err)
			}
			if err := checkSecretGitTracked(tracked, "no-db"); err != nil {
				t.Errorf("non-secret key refused: %v", err)
			}
		})
	}
}

// TestSecretGitTrackingSeesBareRepoWithExternalWorkTree pins the fail-CLOSED
// polarity of the credential guard. `isGitTracked` backs
// CheckSecretKeyGitSafety, whose own doc says it is a security control, so
// "could not tell" must never read as "safe to write the secret".
//
// A bare repository with an external work tree is visible ONLY through the
// inherited GIT_DIR/GIT_WORK_TREE pair -- there is no .git in the work tree
// for the scrubbed probe to find. With a scrub-only probe `git ls-files
// --error-unmatch` exits 128 there, isGitTracked returns false, and `bd config
// set linear.api_key <secret>` happily writes the credential into a
// git-tracked config.yaml. The inherited fallback is what closes that.
func TestSecretGitTrackingSeesBareRepoWithExternalWorkTree(t *testing.T) {
	root := t.TempDir()
	bare := filepath.Join(root, "bare.git")
	work := filepath.Join(root, "work")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	runConfigProbeGit(t, root, "init", "--bare", "--quiet", bare)

	tracked := filepath.Join(work, "config.yaml")
	untracked := filepath.Join(work, "untracked.yaml")
	for _, path := range []string{tracked, untracked} {
		if err := os.WriteFile(path, []byte("json: false\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	// The work tree is not a repository on its own; only this env pair binds
	// the two together, which is exactly the view the scrub removes.
	t.Setenv("GIT_DIR", bare)
	t.Setenv("GIT_WORK_TREE", work)
	runConfigProbeGitInheritingRouting(t, work, "add", "--", tracked)

	if !isGitTracked(tracked) {
		t.Error("tracked file in a bare repo with an external work tree reported as untracked: the credential guard is fail-open")
	}
	if isGitTracked(untracked) {
		t.Error("untracked file reported as tracked")
	}
	if err := checkSecretGitTracked(tracked, "linear.api_key"); err == nil || !strings.Contains(err.Error(), "refusing to write secret key") {
		t.Errorf("tracked secret refusal = %v, want a refusal", err)
	}
	if err := checkSecretGitTracked(untracked, "linear.api_key"); err != nil {
		t.Errorf("untracked secret refused: %v", err)
	}
}

// Fixture setup uses the same routing boundary before any per-case poison is set.
func runConfigProbeGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = gitenv.ScrubRouting(os.Environ())
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, output)
	}
}

// runConfigProbeGitInheritingRouting is the deliberate counterpart: the
// bare-repo fixture is only reachable through the inherited GIT_DIR /
// GIT_WORK_TREE pair, so scrubbing here would fail to build it at all.
func runConfigProbeGitInheritingRouting(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, output)
	}
}

func TestSecretGitTrackingHonorsInheritedBareWorkTree(t *testing.T) {
	gitDir, workTree := t.TempDir(), t.TempDir()
	runConfigProbeGit(t, workTree, "init", "--bare", "--quiet", gitDir)
	tracked := filepath.Join(workTree, "config.yaml")
	untracked := filepath.Join(workTree, "untracked.yaml")
	for _, path := range []string{tracked, untracked} {
		if err := os.WriteFile(path, []byte("json: false\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	runConfigProbeGit(t, workTree, "--git-dir", gitDir, "--work-tree", workTree, "add", "--", tracked)
	t.Setenv("GIT_DIR", gitDir)
	t.Setenv("GIT_WORK_TREE", workTree)

	// Fixture guard: the tracked file must be reachable only through the
	// inherited routing, and the scrubbed probe must fail for a configuration
	// reason (no reachable repository) rather than "path is not tracked"
	// (exit 1), which is final and would leave the fallback unreachable.
	probe := func(env []string) error {
		cmd := exec.Command("git", "ls-files", "--error-unmatch", tracked)
		cmd.Dir = workTree
		cmd.Env = env
		return cmd.Run()
	}
	inherited := os.Environ()
	scrubbedErr := probe(gitenv.ScrubRouting(inherited))
	if scrubbedErr == nil {
		t.Fatal("fixture must require inherited routing, scrubbed probe found the file")
	}
	if exit, ok := scrubbedErr.(*exec.ExitError); ok && exit.ExitCode() == 1 {
		t.Fatalf("fixture must fail the scrubbed probe for a configuration reason, got exit 1")
	}
	if err := probe(inherited); err != nil {
		t.Fatalf("inherited tracking precondition: %v", err)
	}

	if !isGitTracked(tracked) {
		t.Error("bare work tree tracking was ignored, secret writes are no longer refused")
	}
	if isGitTracked(untracked) {
		t.Error("untracked file reported as tracked")
	}
	if err := checkSecretGitTracked(tracked, "linear.api_key"); err == nil || !strings.Contains(err.Error(), "refusing to write secret key") {
		t.Errorf("tracked secret refusal = %v", err)
	}
	if err := checkSecretGitTracked(untracked, "linear.api_key"); err != nil {
		t.Errorf("untracked secret refused: %v", err)
	}
}
