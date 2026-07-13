// Package gitworktree creates per-Discord-thread git worktrees so concurrent
// Grok runs on the same project do not share a working directory.
package gitworktree

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Tree is an isolated working copy for one Discord thread.
type Tree struct {
	// Path is the worktree directory (grok --cwd).
	Path string
	// Branch is the dedicated branch checked out in the worktree.
	Branch string
	// Repo is the main project repository path.
	Repo string
}

// BranchName returns the git branch used for a Discord thread.
func BranchName(threadID string) string {
	return "grok/discord/" + threadID
}

// WorktreePath returns the on-disk path for a thread's worktree under dataDir.
func WorktreePath(dataDir, project, threadID string) string {
	return filepath.Join(dataDir, "worktrees", sanitizePathSegment(project), sanitizePathSegment(threadID))
}

// IsRepo reports whether dir is inside a git working tree.
func IsRepo(dir string) bool {
	cmd := exec.Command("git", "-C", dir, "rev-parse", "--is-inside-work-tree")
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == "true"
}

// Ensure returns an existing worktree for the thread or creates one from HEAD
// of the main repo. path is under dataDir/worktrees/<project>/<threadID>.
func Ensure(ctx context.Context, repo, dataDir, project, threadID string) (Tree, error) {
	if repo == "" || threadID == "" {
		return Tree{}, fmt.Errorf("repo and threadID are required")
	}
	if !IsRepo(repo) {
		return Tree{}, fmt.Errorf("not a git repository: %s", repo)
	}

	branch := BranchName(threadID)
	path := WorktreePath(dataDir, project, threadID)
	t := Tree{Path: path, Branch: branch, Repo: repo}

	if ok, err := isUsableWorktree(ctx, repo, path); err != nil {
		return Tree{}, err
	} else if ok {
		log.Printf("gitworktree: reuse path=%s branch=%s", path, branch)
		return t, nil
	}

	// Broken leftover path: remove before recreate.
	if _, err := os.Stat(path); err == nil {
		log.Printf("gitworktree: removing unusable path %s", path)
		_ = Remove(ctx, repo, path, branch)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return Tree{}, fmt.Errorf("mkdir worktree parent: %w", err)
	}

	// Prefer new branch from main repo HEAD.
	err := runGit(ctx, repo, "worktree", "add", "-b", branch, path, "HEAD")
	if err != nil {
		// Branch may already exist without a worktree (previous partial cleanup).
		if branchExists(ctx, repo, branch) {
			err = runGit(ctx, repo, "worktree", "add", path, branch)
		}
		if err != nil {
			return Tree{}, fmt.Errorf("git worktree add: %w", err)
		}
	}

	log.Printf("gitworktree: created path=%s branch=%s repo=%s", path, branch, repo)
	return t, nil
}

// Remove detaches the worktree and deletes the branch. Safe if either is missing.
func Remove(ctx context.Context, repo, path, branch string) error {
	var errs []string

	if path != "" {
		if _, err := os.Stat(path); err == nil {
			if err := runGit(ctx, repo, "worktree", "remove", "--force", path); err != nil {
				// Fallback: unlock + delete directory if git refuses.
				_ = runGit(ctx, repo, "worktree", "prune")
				if rmErr := os.RemoveAll(path); rmErr != nil {
					errs = append(errs, fmt.Sprintf("remove path: %v (git: %v)", rmErr, err))
				} else {
					_ = runGit(ctx, repo, "worktree", "prune")
				}
			}
		}
	}

	if branch != "" && repo != "" && branchExists(ctx, repo, branch) {
		if err := runGit(ctx, repo, "branch", "-D", branch); err != nil {
			errs = append(errs, fmt.Sprintf("delete branch %s: %v", branch, err))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return nil
}

func isUsableWorktree(ctx context.Context, repo, path string) (bool, error) {
	st, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	if !st.IsDir() {
		return false, nil
	}
	if !IsRepo(path) {
		return false, nil
	}
	// Must belong to the same repository (common git dir).
	mainCommon, err := gitOutput(ctx, repo, "rev-parse", "--git-common-dir")
	if err != nil {
		return false, err
	}
	wtCommon, err := gitOutput(ctx, path, "rev-parse", "--git-common-dir")
	if err != nil {
		return false, nil
	}
	mainAbs, err := absGitPath(repo, mainCommon)
	if err != nil {
		return false, err
	}
	wtAbs, err := absGitPath(path, wtCommon)
	if err != nil {
		return false, nil
	}
	return mainAbs == wtAbs, nil
}

func absGitPath(base, p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		return "", fmt.Errorf("empty git path")
	}
	if filepath.IsAbs(p) {
		return filepath.Clean(p), nil
	}
	return filepath.Abs(filepath.Join(base, p))
}

func branchExists(ctx context.Context, repo, branch string) bool {
	err := runGit(ctx, repo, "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
	return err == nil
}

func runGit(ctx context.Context, dir string, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("git %s: %s", strings.Join(args, " "), msg)
	}
	return nil
}

func gitOutput(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), msg)
	}
	return strings.TrimSpace(stdout.String()), nil
}

func sanitizePathSegment(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "_unknown"
	}
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := b.String()
	if out == "." || out == ".." || out == "" {
		return "_unknown"
	}
	return out
}
