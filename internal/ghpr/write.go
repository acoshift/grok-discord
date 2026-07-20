package ghpr

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// MergeMethod is a gh pr merge strategy.
type MergeMethod string

const (
	MergeSquash MergeMethod = "squash"
	MergeMerge  MergeMethod = "merge"
	MergeRebase MergeMethod = "rebase"
)

// NormalizeMergeMethod returns squash/merge/rebase (default squash).
func NormalizeMergeMethod(m string) MergeMethod {
	switch strings.ToLower(strings.TrimSpace(m)) {
	case "merge":
		return MergeMerge
	case "rebase":
		return MergeRebase
	default:
		return MergeSquash
	}
}

// CommentIssue posts a comment on a GitHub issue via body-file.
func CommentIssue(ctx context.Context, repoDir, owner, repo string, number int, body string) error {
	return CommentIssueWith(ctx, defaultRunner, repoDir, owner, repo, number, body)
}

// CommentIssueWith is CommentIssue with an injectable runner.
func CommentIssueWith(ctx context.Context, run Runner, repoDir, owner, repo string, number int, body string) error {
	if run == nil {
		run = defaultRunner
	}
	body = strings.TrimSpace(body)
	if body == "" {
		return fmt.Errorf("empty comment body")
	}
	if number <= 0 {
		return fmt.Errorf("invalid issue number")
	}
	path, cleanup, err := writeBodyFile(body)
	if err != nil {
		return err
	}
	defer cleanup()
	args := []string{"issue", "comment", strconv.Itoa(number), "--body-file", path}
	if o, r := strings.TrimSpace(owner), strings.TrimSpace(repo); o != "" && r != "" {
		args = append(args, "--repo", o+"/"+r)
	}
	_, err = run(ctx, repoDir, "gh", args...)
	return err
}

// CommentPR posts a comment on a pull request via body-file.
func CommentPR(ctx context.Context, repoDir, owner, repo string, number int, body string) error {
	return CommentPRWith(ctx, defaultRunner, repoDir, owner, repo, number, body)
}

// CommentPRWith is CommentPR with an injectable runner.
func CommentPRWith(ctx context.Context, run Runner, repoDir, owner, repo string, number int, body string) error {
	if run == nil {
		run = defaultRunner
	}
	body = strings.TrimSpace(body)
	if body == "" {
		return fmt.Errorf("empty comment body")
	}
	if number <= 0 {
		return fmt.Errorf("invalid PR number")
	}
	path, cleanup, err := writeBodyFile(body)
	if err != nil {
		return err
	}
	defer cleanup()
	args := []string{"pr", "comment", strconv.Itoa(number), "--body-file", path}
	if o, r := strings.TrimSpace(owner), strings.TrimSpace(repo); o != "" && r != "" {
		args = append(args, "--repo", o+"/"+r)
	}
	_, err = run(ctx, repoDir, "gh", args...)
	return err
}

// ClosePR closes a pull request (no comment required).
func ClosePR(ctx context.Context, repoDir, owner, repo string, number int) error {
	return ClosePRWith(ctx, defaultRunner, repoDir, owner, repo, number)
}

// ClosePRWith is ClosePR with an injectable runner.
func ClosePRWith(ctx context.Context, run Runner, repoDir, owner, repo string, number int) error {
	if run == nil {
		run = defaultRunner
	}
	if number <= 0 {
		return fmt.Errorf("invalid PR number")
	}
	args := []string{"pr", "close", strconv.Itoa(number)}
	if o, r := strings.TrimSpace(owner), strings.TrimSpace(repo); o != "" && r != "" {
		args = append(args, "--repo", o+"/"+r)
	}
	_, err := run(ctx, repoDir, "gh", args...)
	return err
}

// MergeOpts controls gh pr merge (never includes bypass flags).
type MergeOpts struct {
	Method         MergeMethod
	AttemptAnyway  bool // allow when checks failing; still no --admin
}

// MergePreflight is the pure allow/deny decision before calling gh.
type MergePreflight struct {
	Allow  bool
	Reason string
}

// CheckMergePreflight decides whether a merge may proceed.
// Never authorizes bypass of GitHub branch protection; only gates our call.
func CheckMergePreflight(state, mergeable, checks string, attemptAnyway bool) MergePreflight {
	st := strings.ToUpper(strings.TrimSpace(state))
	if st != "OPEN" {
		return MergePreflight{Allow: false, Reason: "PR is not OPEN (state=" + st + ")"}
	}
	m := strings.ToUpper(strings.TrimSpace(mergeable))
	if m == "CONFLICTING" {
		return MergePreflight{Allow: false, Reason: "PR has merge conflicts"}
	}
	if ChecksFailing(checks) && !attemptAnyway {
		return MergePreflight{Allow: false, Reason: "checks failing; enable attempt anyway to retry plain merge"}
	}
	return MergePreflight{Allow: true}
}

// ChecksFailing reports whether a checks rollup string indicates failures.
func ChecksFailing(checks string) bool {
	c := strings.TrimSpace(checks)
	if c == "" || c == "none" {
		return false
	}
	// SummarizeChecks format: "✓ n · ✗ n · … n"
	return strings.Contains(c, "✗") || strings.Contains(strings.ToLower(c), "fail")
}

// MergePR merges a PR with the given method (default squash). Never passes --admin.
func MergePR(ctx context.Context, repoDir, owner, repo string, number int, opts MergeOpts) error {
	return MergePRWith(ctx, defaultRunner, repoDir, owner, repo, number, opts)
}

// MergePRWith is MergePR with an injectable runner.
func MergePRWith(ctx context.Context, run Runner, repoDir, owner, repo string, number int, opts MergeOpts) error {
	if run == nil {
		run = defaultRunner
	}
	if number <= 0 {
		return fmt.Errorf("invalid PR number")
	}
	method := NormalizeMergeMethod(string(opts.Method))
	args := []string{"pr", "merge", strconv.Itoa(number), "--" + string(method)}
	if o, r := strings.TrimSpace(owner), strings.TrimSpace(repo); o != "" && r != "" {
		args = append(args, "--repo", o+"/"+r)
	}
	// Explicitly never add --admin, --disable-auto, etc. that weaken protection.
	for _, a := range args {
		if a == "--admin" || strings.Contains(a, "bypass") {
			return fmt.Errorf("refusing merge args that bypass protection")
		}
	}
	_, err := run(ctx, repoDir, "gh", args...)
	return err
}

func writeBodyFile(body string) (path string, cleanup func(), err error) {
	dir, err := os.MkdirTemp("", "ghpr-body-*")
	if err != nil {
		return "", func() {}, err
	}
	path = filepath.Join(dir, "body.md")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		_ = os.RemoveAll(dir)
		return "", func() {}, err
	}
	return path, func() { _ = os.RemoveAll(dir) }, nil
}
