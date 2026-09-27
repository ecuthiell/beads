package gitenv

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// allRedirectKeys and allFenceKeys are the classification contract. A key that
// moves between them changes whether scrubbing narrows or widens Git's
// authority, so the move has to be made here, deliberately, rather than fall
// out of an edit to the maps.
var (
	allRedirectKeys = []string{
		"GIT_ALTERNATE_OBJECT_DIRECTORIES",
		"GIT_COMMON_DIR",
		"GIT_DIR",
		"GIT_DISCOVERY_ACROSS_FILESYSTEM",
		"GIT_EXEC_PATH",
		"GIT_GRAFT_FILE",
		"GIT_IMPLICIT_WORK_TREE",
		"GIT_INDEX_FILE",
		"GIT_INTERNAL_SUPER_PREFIX",
		"GIT_NAMESPACE",
		"GIT_OBJECT_DIRECTORY",
		"GIT_PREFIX",
		"GIT_QUARANTINE_PATH",
		"GIT_REPLACE_REF_BASE",
		"GIT_SHALLOW_FILE",
		"GIT_SUPER_PREFIX",
		"GIT_TEMPLATE_DIR",
		"GIT_WORK_TREE",
		// Injected configuration is a redirect of config authority, so the
		// GIT_CONFIG prefix rule must still reach these.
		"GIT_CONFIG_COUNT",
		"GIT_CONFIG_KEY_0",
		"GIT_CONFIG_VALUE_0",
		"GIT_CONFIG_PARAMETERS",
	}
	allFenceKeys = []string{
		"GIT_CEILING_DIRECTORIES",
		"GIT_CONFIG_GLOBAL",
		"GIT_CONFIG_NOSYSTEM",
		"GIT_CONFIG_SYSTEM",
	}
)

func TestScrubRoutingRemovesRedirectsAndPreservesFences(t *testing.T) {
	for _, goos := range []string{"linux", "windows"} {
		for _, key := range allRedirectKeys {
			t.Run(goos+"/redirect/"+key, func(t *testing.T) {
				if !IsRedirectKeyForOS(key, goos) {
					t.Fatalf("IsRedirectKeyForOS(%q) = false, want true", key)
				}
				if IsFenceKeyForOS(key, goos) {
					t.Fatalf("IsFenceKeyForOS(%q) = true, want false", key)
				}
				got := ScrubRoutingForOS([]string{key + "=poison", "KEEP=1"}, goos)
				if !slices.Equal(got, []string{"KEEP=1"}) {
					t.Fatalf("ScrubRoutingForOS kept redirect %s: %q", key, got)
				}
			})
		}
		for _, key := range allFenceKeys {
			t.Run(goos+"/fence/"+key, func(t *testing.T) {
				if IsRedirectKeyForOS(key, goos) {
					t.Fatalf("IsRedirectKeyForOS(%q) = true, want false: scrubbing a fence widens Git's authority", key)
				}
				if !IsFenceKeyForOS(key, goos) {
					t.Fatalf("IsFenceKeyForOS(%q) = false, want true", key)
				}
				input := []string{key + "=fenced", "KEEP=1"}
				got := ScrubRoutingForOS(input, goos)
				if !slices.Equal(got, input) {
					t.Fatalf("ScrubRoutingForOS dropped fence %s: %q", key, got)
				}
			})
		}
	}
}

// GIT_DISCOVERY_ACROSS_FILESYSTEM reads like a fence but is the opposite: Git
// stops the discovery walk at a filesystem boundary UNLESS it is set. Git says
// so itself -- "Stopping at filesystem boundary
// (GIT_DISCOVERY_ACROSS_FILESYSTEM not set)". Removing it therefore restores
// the narrower default, which is why it is classified as a redirect. Moving it
// to the fence set would preserve a caller-supplied widening.
func TestDiscoveryAcrossFilesystemIsARedirectNotAFence(t *testing.T) {
	for _, goos := range []string{"linux", "windows"} {
		if IsFenceKeyForOS("GIT_DISCOVERY_ACROSS_FILESYSTEM", goos) {
			t.Errorf("%s: GIT_DISCOVERY_ACROSS_FILESYSTEM classified as a fence; unsetting it NARROWS discovery", goos)
		}
	}
}

func TestClearRoutingPreservesFences(t *testing.T) {
	restoreRedirectEnv(t)

	t.Setenv("GIT_DIR", "/wrong")
	t.Setenv("GIT_CEILING_DIRECTORIES", "/fenced")
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")

	removed, err := ClearRouting()
	if err != nil {
		t.Fatal(err)
	}
	if !removed {
		t.Fatal("ClearRouting() did not report removing redirect entries")
	}
	if _, ok := os.LookupEnv("GIT_DIR"); ok {
		t.Fatal("GIT_DIR remains set")
	}
	for key, want := range map[string]string{
		"GIT_CEILING_DIRECTORIES": "/fenced",
		"GIT_CONFIG_GLOBAL":       os.DevNull,
		"GIT_CONFIG_NOSYSTEM":     "1",
	} {
		if got := os.Getenv(key); got != want {
			t.Errorf("%s = %q, want %q: clearing a fence widens the whole process", key, got, want)
		}
	}
}

// TestScrubbedCommandCannotEscapeAnInheritedCeiling is the behavioral half of
// the classification: it proves that a role WRITE run with a scrubbed
// environment still honors an inherited discovery ceiling instead of climbing
// into the containing repository the operator fenced off. Dropping
// GIT_CEILING_DIRECTORIES from the scrub set turns the refusal below into a
// silent exit 0 that writes beads.role into parent/.git/config.
func TestScrubbedCommandCannotEscapeAnInheritedCeiling(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("GIT_CEILING_DIRECTORIES path semantics differ on Windows")
	}
	parent := t.TempDir()
	child := filepath.Join(parent, "child")
	sub := filepath.Join(child, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	// Only `parent` is a repository; `child` and `sub` are ordinary directories,
	// so discovery from `sub` reaches `parent` unless a ceiling stops it.
	initCmd := exec.Command("git", "init", "--quiet")
	initCmd.Dir = parent
	initCmd.Env = ScrubRouting(os.Environ())
	if output, err := initCmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, output)
	}

	t.Setenv("GIT_CEILING_DIRECTORIES", child)
	write := exec.Command("git", "config", "beads.role", "maintainer")
	write.Dir = sub
	write.Env = ScrubRouting(os.Environ())
	output, err := write.CombinedOutput()
	if err == nil {
		t.Fatalf("scrubbed role write succeeded above the ceiling; output: %s", output)
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 128 {
		t.Fatalf("scrubbed role write error = %v, want exit 128", err)
	}

	config, readErr := os.ReadFile(filepath.Join(parent, ".git", "config"))
	if readErr != nil {
		t.Fatal(readErr)
	}
	if strings.Contains(string(config), "role") {
		t.Fatalf("role escaped into the fenced parent repository:\n%s", config)
	}
}

func restoreRedirectEnv(t *testing.T) {
	t.Helper()
	type envEntry struct{ key, value string }
	var inherited []envEntry
	for _, entry := range os.Environ() {
		key := EntryKey(entry)
		if !IsRedirectKeyForOS(key, runtime.GOOS) {
			continue
		}
		value, _ := os.LookupEnv(key)
		inherited = append(inherited, envEntry{key: key, value: value})
	}
	t.Cleanup(func() {
		for _, entry := range os.Environ() {
			key := EntryKey(entry)
			if IsRedirectKeyForOS(key, runtime.GOOS) {
				if err := os.Unsetenv(key); err != nil {
					t.Errorf("unset %s during cleanup: %v", key, err)
				}
			}
		}
		for _, entry := range inherited {
			if err := os.Setenv(entry.key, entry.value); err != nil {
				t.Errorf("restore %s during cleanup: %v", entry.key, err)
			}
		}
	})
}
