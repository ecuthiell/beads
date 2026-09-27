package main

import (
	"os"
	"runtime"
	"testing"

	"github.com/steveyegge/beads/internal/gitenv"
)

// isolateInheritedGitEnv makes a fixture hermetic against the host's Git
// environment: it removes every inherited redirect AND fence key, then
// re-asserts the one boundary the fixture still needs for itself.
//
// Clearing the fences is the point of this helper. GIT_CONFIG_GLOBAL and
// GIT_CONFIG_SYSTEM outrank HOME, so on a host that exports one, a fixture
// that pins HOME and writes its own ~/.gitconfig silently reads the host's
// file instead. ScrubRouting preserves fences by design -- removing a fence
// widens Git's authority rather than narrowing it -- so a hermetic baseline
// has to drop them here and then assert its own boundary, the way
// internal/storage/domain/git's suite fixture does. GIT_CONFIG_NOSYSTEM=1 is
// that assertion: without it, dropping an inherited GIT_CONFIG_NOSYSTEM would
// trade a dependency on the host's global config for one on /etc/gitconfig. A
// fixture that needs some other fence sets it for itself after calling this.
func isolateInheritedGitEnv(t *testing.T) {
	t.Helper()
	for _, entry := range os.Environ() {
		key := gitenv.EntryKey(entry)
		if !gitenv.IsRedirectKeyForOS(key, runtime.GOOS) && !gitenv.IsFenceKeyForOS(key, runtime.GOOS) {
			continue
		}
		// Setenv registers restoration; Unsetenv then makes the key absent.
		t.Setenv(key, "")
		if err := os.Unsetenv(key); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
}
