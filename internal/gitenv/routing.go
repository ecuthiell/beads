// Package gitenv defines Git environment boundaries shared by command and
// startup discovery code.
package gitenv

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/steveyegge/beads/internal/execenv"
)

// redirectKeys name variables that point Git at a repository, index, object
// store, namespace, executable, template, or config source other than the one
// the command's working directory implies. Removing a redirect NARROWS Git's
// authority back to that working directory, which is the whole point of the
// scrub.
//
// GIT_DISCOVERY_ACROSS_FILESYSTEM belongs here even though it names no path:
// Git stops the discovery walk at a filesystem boundary unless it is set, so
// removing it restores the narrower default. Git states the direction itself
// when the walk stops — "Stopping at filesystem boundary
// (GIT_DISCOVERY_ACROSS_FILESYSTEM not set)".
var redirectKeys = map[string]struct{}{
	"GIT_ALTERNATE_OBJECT_DIRECTORIES": {},
	"GIT_COMMON_DIR":                   {},
	"GIT_DIR":                          {},
	"GIT_DISCOVERY_ACROSS_FILESYSTEM":  {},
	"GIT_EXEC_PATH":                    {},
	"GIT_GRAFT_FILE":                   {},
	"GIT_IMPLICIT_WORK_TREE":           {},
	"GIT_INDEX_FILE":                   {},
	"GIT_INTERNAL_SUPER_PREFIX":        {},
	"GIT_NAMESPACE":                    {},
	"GIT_OBJECT_DIRECTORY":             {},
	"GIT_PREFIX":                       {},
	"GIT_QUARANTINE_PATH":              {},
	"GIT_REPLACE_REF_BASE":             {},
	"GIT_SHALLOW_FILE":                 {},
	"GIT_SUPER_PREFIX":                 {},
	"GIT_TEMPLATE_DIR":                 {},
	"GIT_WORK_TREE":                    {},
}

// fenceKeys name variables that RESTRICT Git's authority. Removing a fence
// WIDENS it, which is the opposite of what this package exists to do:
// dropping GIT_CEILING_DIRECTORIES lets the discovery walk climb into a
// containing parent repository, and dropping GIT_CONFIG_GLOBAL,
// GIT_CONFIG_SYSTEM, or GIT_CONFIG_NOSYSTEM re-admits the host's real
// ~/.gitconfig and /etc/gitconfig into a command the caller had deliberately
// isolated. The scrub therefore preserves them.
//
// A caller that wants a boundary stricter than the one it inherited must
// assert that boundary explicitly rather than rely on removal — see
// newWorktreeRemovalGit, which drops these via IsFenceKeyForOS and then
// re-pins GIT_CONFIG_GLOBAL/_SYSTEM to os.DevNull and GIT_CONFIG_NOSYSTEM to 1.
var fenceKeys = map[string]struct{}{
	"GIT_CEILING_DIRECTORIES": {},
	"GIT_CONFIG_GLOBAL":       {},
	"GIT_CONFIG_NOSYSTEM":     {},
	"GIT_CONFIG_SYSTEM":       {},
}

// Derive Windows membership once from the canonical key sets.
var (
	windowsRedirectKeys = windowsIdentitySet(redirectKeys)
	windowsFenceKeys    = windowsIdentitySet(fenceKeys)
	windowsConfigPrefix = execenv.KeyIdentityForOS("GIT_CONFIG", "windows")
)

func windowsIdentitySet(keys map[string]struct{}) map[string]struct{} {
	windows := make(map[string]struct{}, len(keys))
	for key := range keys {
		windows[execenv.KeyIdentityForOS(key, "windows")] = struct{}{}
	}
	return windows
}

// EntryKey returns the key portion using the shared subprocess split rule.
func EntryKey(entry string) string {
	return execenv.EntryKey(entry)
}

// IsRedirectKeyForOS reports whether key can redirect Git away from an
// explicit working directory or alter its repository, index, object,
// namespace, executable, template, or config authority. Fence keys are
// excluded: they restrict Git rather than redirect it, so removing them widens
// authority instead of narrowing it. Environment names follow host semantics:
// byte-exact on POSIX and case-insensitive on Windows.
func IsRedirectKeyForOS(key, goos string) bool {
	keys, fences, prefix := redirectKeys, fenceKeys, "GIT_CONFIG"
	if goos == "windows" {
		keys, fences, prefix = windowsRedirectKeys, windowsFenceKeys, windowsConfigPrefix
	}
	key = execenv.KeyIdentityForOS(key, goos)
	if _, fence := fences[key]; fence {
		return false
	}
	// The remaining GIT_CONFIG* names (GIT_CONFIG_COUNT, GIT_CONFIG_KEY_<n>,
	// GIT_CONFIG_VALUE_<n>, GIT_CONFIG_PARAMETERS) inject configuration
	// directly, so they redirect config authority and are swept by prefix.
	if strings.HasPrefix(key, prefix) {
		return true
	}
	_, redirect := keys[key]
	return redirect
}

// IsFenceKeyForOS reports whether key restricts Git's repository-discovery or
// configuration authority, so that removing it would widen that authority.
// ScrubRoutingForOS preserves these; callers needing a stricter boundary use
// this to drop them and then re-assert the boundary themselves.
func IsFenceKeyForOS(key, goos string) bool {
	fences := fenceKeys
	if goos == "windows" {
		fences = windowsFenceKeys
	}
	_, fence := fences[execenv.KeyIdentityForOS(key, goos)]
	return fence
}

// ScrubRouting removes Git redirect entries using the current host's
// environment-key semantics.
func ScrubRouting(env []string) []string {
	return ScrubRoutingForOS(env, runtime.GOOS)
}

// ScrubRoutingForOS removes Git redirect entries using goos environment-key
// semantics. It preserves non-routing controls such as GIT_OPTIONAL_LOCKS and
// GIT_NO_REPLACE_OBJECTS, and it preserves the discovery and config fences
// named by IsFenceKeyForOS — scrubbing those would hand Git back the authority
// the caller fenced off.
func ScrubRoutingForOS(env []string, goos string) []string {
	cleaned := make([]string, 0, len(env))
	for _, entry := range env {
		if IsRedirectKeyForOS(EntryKey(entry), goos) {
			continue
		}
		cleaned = append(cleaned, entry)
	}
	return cleaned
}

// ClearRouting permanently removes Git redirect entries from the current
// process. CLI callers use it as a command-lifetime authority boundary. It
// leaves fence keys in place, so a caller that isolated this process from the
// host's global config stays isolated. It reports whether any entry was
// removed.
//
// Every redirect key is attempted even when one Unsetenv fails, so a failure
// cannot leave the process half-cleared with the remaining keys still live.
func ClearRouting() (bool, error) {
	removed := false
	var errs []error
	for _, entry := range os.Environ() {
		key := EntryKey(entry)
		if !IsRedirectKeyForOS(key, runtime.GOOS) {
			continue
		}
		if err := os.Unsetenv(key); err != nil {
			errs = append(errs, fmt.Errorf("unset %s: %w", key, err))
			continue
		}
		removed = true
	}
	return removed, errors.Join(errs...)
}
