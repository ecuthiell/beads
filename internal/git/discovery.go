package git

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/steveyegge/beads/internal/gitenv"
)

// RevParseGitDirs runs `git -C repoPath rev-parse --git-dir --git-common-dir`
// and returns the two lines Git printed, trimmed but otherwise verbatim: Git
// may answer with repository-relative paths, and callers canonicalize them
// against repoPath with their own rules.
//
// repoPath is the authority for this probe, so inherited Git redirect routing
// is scrubbed. GIT_DIR and GIT_COMMON_DIR outrank -C, which means an
// unscrubbed copy of this probe answers for a different repository than the
// one the caller named. That matters because bd asks this same question from
// three places during startup — which config.yaml owns the repo, which .beads
// directory backs it, and whether a path is a linked worktree — and a copy
// that disagreed would resolve the config from one repository and the database
// from another. Routing every copy through here makes that divergence
// impossible to reintroduce one call site at a time.
//
// Discovery and config fences are preserved by gitenv.ScrubRouting, so a
// caller that fenced this process off from the host's global config or capped
// the discovery walk keeps that boundary here.
func RevParseGitDirs(repoPath string) (gitDir, commonDir string, err error) {
	cmd := exec.Command("git", "-C", repoPath, "rev-parse", "--git-dir", "--git-common-dir")
	cmd.Env = gitenv.ScrubRouting(os.Environ())
	output, err := cmd.Output()
	if err != nil {
		return "", "", fmt.Errorf("git rev-parse --git-dir --git-common-dir in %s: %w", repoPath, err)
	}

	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(lines) < 2 {
		return "", "", fmt.Errorf("git rev-parse --git-dir --git-common-dir in %s: expected 2 lines, got %d", repoPath, len(lines))
	}

	return strings.TrimSpace(lines[0]), strings.TrimSpace(lines[1]), nil
}
