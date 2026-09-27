// Package gitenv defines Git environment boundaries shared by command and
// startup discovery code.
package gitenv

import (
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/steveyegge/beads/internal/execenv"
)

// routingKeys name Git environment variables that redirect Git away from an
// explicit working directory, or replace its repository, index, object,
// namespace, executable, template, or config authority. Scrubbing one of these
// narrows the authority a child process inherits, which is the point of this
// package.
//
// Git's two discovery-boundary variables are both deliberately absent, but for
// opposite reasons, because their polarity is opposite.
//
// GIT_CEILING_DIRECTORIES withholds authority: it bounds how far Git may walk
// up from its starting directory, so removing it widens repository selection
// instead of narrowing it. Under GIT_CEILING_DIRECTORIES=$P/child, the command
// `git -C $P/child/sub config beads.role maintainer` is refused (exit 128), but
// with the ceiling scrubbed it succeeds and writes the privileged role into
// $P/.git/config. Every call site here is a discovery call against a path that
// is not guaranteed to be a repository root, and -C names a directory rather
// than a repository, so an explicit directory does not substitute for the
// fence. A caller that wants its working directory to be the boundary must
// supply its own ceiling -- GIT_CEILING_DIRECTORIES=<parent of the intended
// root> -- instead of deleting the operator's. newWorktreeRemovalGit in
// cmd/bd/worktree_cmd.go is the precedent for that supply-your-own pattern,
// but only on the config plane (GIT_CONFIG_GLOBAL/_SYSTEM/_NOSYSTEM); it
// supplies no discovery fence, so it is not an example of this form.
//
// GIT_DISCOVERY_ACROSS_FILESYSTEM is permit-only, so the direction claim above
// does not apply to it: Git already stops at a filesystem boundary, unset *is*
// the stop, and the variable exists only to disable that stop. Retaining it
// therefore narrows nothing -- an inherited =1 can only widen discovery across
// a mount. It is kept out of the set anyway, so that an operator or a
// legitimate cross-mount checkout that sets it keeps working under every bd
// verb instead of losing the verb outright. That is a deliberate trade with a
// real cost: base scrubbed both keys in scrubWorktreeRemovalGitEnv's own
// hardcoded list, so routing that adapter through this shared set reverses
// base policy for this key and lets `bd worktree remove` discovery cross a
// mount boundary where base stopped it. Tracked in bd-dz16y; revisit if that
// widening ever outweighs the cross-mount caller.
var routingKeys = map[string]struct{}{
	"GIT_ALTERNATE_OBJECT_DIRECTORIES": {},
	"GIT_COMMON_DIR":                   {},
	"GIT_DIR":                          {},
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

// Derive Windows membership once from the canonical routing-key set.
var windowsRoutingKeys = func() map[string]struct{} {
	keys := make(map[string]struct{}, len(routingKeys))
	for key := range routingKeys {
		keys[execenv.KeyIdentityForOS(key, "windows")] = struct{}{}
	}
	return keys
}()

var windowsConfigPrefix = execenv.KeyIdentityForOS("GIT_CONFIG", "windows")

// EntryKey returns the key portion using the shared subprocess split rule.
func EntryKey(entry string) string {
	return execenv.EntryKey(entry)
}

// IsRoutingKeyForOS reports whether key can redirect Git away from an explicit
// working directory or alter its repository, index, object, namespace,
// executable, template, or config authority. Environment names follow host
// semantics: byte-exact on POSIX and case-insensitive on Windows.
//
// The GIT_CONFIG prefix still covers the suppression knobs GIT_CONFIG_NOSYSTEM,
// GIT_CONFIG_GLOBAL and GIT_CONFIG_SYSTEM, which fence config authority rather
// than redirect it. Retaining them cannot be decided by key alone, because
// GIT_CONFIG_GLOBAL=/dev/null suppresses config while GIT_CONFIG_GLOBAL=/tmp/evil
// injects it; that value-aware distinction is made in follow-up #6461. The
// injection channel this package must close -- the GIT_CONFIG_COUNT/KEY_n/VALUE_n
// triple -- is covered either way.
func IsRoutingKeyForOS(key, goos string) bool {
	keys := routingKeys
	prefix := "GIT_CONFIG"
	if goos == "windows" {
		keys = windowsRoutingKeys
		prefix = windowsConfigPrefix
	}
	key = execenv.KeyIdentityForOS(key, goos)
	if strings.HasPrefix(key, prefix) {
		return true
	}
	_, routing := keys[key]
	return routing
}

// ScrubRouting removes Git routing entries using the current host's
// environment-key semantics.
func ScrubRouting(env []string) []string {
	return ScrubRoutingForOS(env, runtime.GOOS)
}

// ScrubRoutingForOS removes Git routing entries using goos environment-key
// semantics. It preserves non-routing controls such as GIT_OPTIONAL_LOCKS and
// GIT_NO_REPLACE_OBJECTS.
func ScrubRoutingForOS(env []string, goos string) []string {
	cleaned := make([]string, 0, len(env))
	for _, entry := range env {
		if IsRoutingKeyForOS(EntryKey(entry), goos) {
			continue
		}
		cleaned = append(cleaned, entry)
	}
	return cleaned
}

// ClearRouting permanently removes Git routing entries from the current
// process. CLI callers use it as a command-lifetime authority boundary. It
// reports whether any entry was removed.
func ClearRouting() (bool, error) {
	removed := false
	for _, entry := range os.Environ() {
		key := EntryKey(entry)
		if !IsRoutingKeyForOS(key, runtime.GOOS) {
			continue
		}
		if err := os.Unsetenv(key); err != nil {
			return removed, fmt.Errorf("unset %s: %w", key, err)
		}
		removed = true
	}
	return removed, nil
}
