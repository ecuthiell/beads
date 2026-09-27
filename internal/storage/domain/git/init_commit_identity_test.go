package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/gitenv"
	"github.com/steveyegge/beads/internal/storage/domain"
	"github.com/stretchr/testify/require"
)

func TestInitCommitKeepsInlineIdentityWithoutRouting(t *testing.T) {
	for _, entry := range os.Environ() {
		key := gitenv.EntryKey(entry)
		if strings.HasPrefix(strings.ToUpper(key), "GIT_") || key == "EMAIL" {
			t.Setenv(key, "")
			require.NoError(t, os.Unsetenv(key))
		}
	}
	home := t.TempDir()
	for _, key := range []string{"HOME", "USERPROFILE", "XDG_CONFIG_HOME"} {
		t.Setenv(key, home)
	}
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	runGit := func(dir string, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir, cmd.Env = dir, gitenv.ScrubRouting(os.Environ())
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "fixture git %v: %s", args, out)
		return strings.TrimSpace(string(out))
	}
	for _, kind := range []string{"inline_only", "local_author_committer", "explicit_env", "invalid_count", "incomplete_table", "inactive_entry"} {
		t.Run(kind, func(t *testing.T) {
			target, decoy := t.TempDir(), t.TempDir()
			for _, dir := range []string{target, decoy} {
				runGit(dir, "init", "--quiet")
				runGit(dir, "config", "--local", "core.hooksPath", ".git/hooks")
				runGit(dir, "config", "--local", "user.useConfigOnly", "true")
				runGit(dir, "config", "--local", "commit.gpgSign", "false")
			}
			runGit(decoy, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.test", "commit", "--allow-empty", "-m", "decoy")
			decoyHead := runGit(decoy, "rev-parse", "HEAD")
			decoyConfig, err := os.ReadFile(filepath.Join(decoy, ".git", "config"))
			require.NoError(t, err)
			require.NoError(t, os.Mkdir(filepath.Join(target, ".beads"), 0755))
			require.NoError(t, os.WriteFile(filepath.Join(target, ".beads", "artifact"), []byte("target\n"), 0600))
			marker := filepath.Join(target, "hook-ran")
			t.Setenv("BEADS_INLINE_IDENTITY_HOOK_MARKER", marker)
			require.NoError(t, os.WriteFile(filepath.Join(target, ".git", "hooks", "post-commit"), []byte("#!/bin/sh\nprintf ran > \"$BEADS_INLINE_IDENTITY_HOOK_MARKER\"\n"), 0755))
			want := "Inline Last|inline@example.test|Inline Last|inline@example.test"
			if kind == "invalid_count" || kind == "incomplete_table" {
				runGit(target, "config", "--local", "user.name", "Local Fallback")
				runGit(target, "config", "--local", "user.email", "fallback@example.test")
				want = "Local Fallback|fallback@example.test|Local Fallback|fallback@example.test"
			}
			if kind == "local_author_committer" {
				for key, value := range map[string]string{
					"author.name": "Local Author", "author.email": "author@example.test",
					"committer.name": "Local Committer", "committer.email": "committer@example.test",
				} {
					runGit(target, "config", "--local", key, value)
				}
				want = "Local Author|author@example.test|Local Committer|committer@example.test"
			}
			if kind == "explicit_env" {
				for key, value := range map[string]string{
					"GIT_AUTHOR_NAME": "Env Author", "GIT_AUTHOR_EMAIL": "env-author@example.test",
					"GIT_COMMITTER_NAME": "Env Committer", "GIT_COMMITTER_EMAIL": "env-committer@example.test",
				} {
					t.Setenv(key, value)
				}
				want = "Env Author|env-author@example.test|Env Committer|env-committer@example.test"
			}
			inline := [][2]string{
				{"user.name", "Inline First"}, {"user.email", "inline@example.test"},
				{"core.bare", "true"}, {"core.worktree", decoy}, {"beads.role", "injected-role"},
				{"UsEr.NaMe", "Inline Last"},
			}
			t.Setenv("GIT_CONFIG_COUNT", strconv.Itoa(len(inline)))
			for i, pair := range inline {
				t.Setenv("GIT_CONFIG_KEY_"+strconv.Itoa(i), pair[0])
				t.Setenv("GIT_CONFIG_VALUE_"+strconv.Itoa(i), pair[1])
			}
			switch kind {
			case "invalid_count":
				t.Setenv("GIT_CONFIG_COUNT", "999999999999999999999")
			case "incomplete_table":
				t.Setenv("GIT_CONFIG_COUNT", strconv.Itoa(len(inline)+1))
			case "inactive_entry":
				t.Setenv("GIT_CONFIG_COUNT", "2")
				want = "Inline First|inline@example.test|Inline First|inline@example.test"
			}
			t.Setenv("GIT_DIR", filepath.Join(decoy, ".git"))
			t.Setenv("GIT_WORK_TREE", decoy)
			foreignIndex := filepath.Join(t.TempDir(), "foreign-index")
			require.NoError(t, os.WriteFile(foreignIndex, []byte("unchanged foreign index"), 0600))
			t.Setenv("GIT_INDEX_FILE", foreignIndex)
			repo := NewInitGitRepository(target)
			// The adapter owns a snapshot; later process changes cannot change it.
			t.Setenv("GIT_CONFIG_VALUE_5", "Later identity")
			env := os.Environ()
			result, err := domain.NewGitUseCase(target, repo).CommitInitArtifacts(t.Context(), domain.CommitInitArtifactsParams{
				BeadsDir: ".beads/", Message: "init artifacts", NoVerify: true, SkipHooks: true,
			})
			require.NoError(t, err)
			require.True(t, result.DidCommit)
			require.Equal(t, want, runGit(target, "log", "-1", "--format=%an|%ae|%cn|%ce"))
			require.Equal(t, "target", runGit(target, "show", "HEAD:.beads/artifact"))
			require.Equal(t, decoyHead, runGit(decoy, "rev-parse", "HEAD"))
			afterConfig, err := os.ReadFile(filepath.Join(decoy, ".git", "config"))
			require.NoError(t, err)
			require.Equal(t, decoyConfig, afterConfig)
			afterIndex, err := os.ReadFile(foreignIndex)
			require.NoError(t, err)
			require.Equal(t, "unchanged foreign index", string(afterIndex))
			_, err = os.Stat(marker)
			require.True(t, os.IsNotExist(err), "init commit executed a hook")
			require.True(t, slices.Equal(env, os.Environ()), "commit changed caller environment")
		})
	}
}
