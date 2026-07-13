package gitworktree

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureReuseAndRemove(t *testing.T) {
	repo := initTestRepo(t)
	data := t.TempDir()
	ctx := context.Background()

	tr, err := Ensure(ctx, repo, data, "app", "111")
	if err != nil {
		t.Fatal(err)
	}
	if tr.Branch != "grok/discord/111" {
		t.Fatalf("branch=%q", tr.Branch)
	}
	if !IsRepo(tr.Path) {
		t.Fatal("worktree not a repo")
	}
	// Write a file only in the worktree — main should not see it as untracked
	// in the same path (different directories).
	marker := filepath.Join(tr.Path, "only-wt.txt")
	if err := os.WriteFile(marker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(repo, "only-wt.txt")); !os.IsNotExist(err) {
		t.Fatal("marker leaked into main worktree path")
	}

	// Reuse
	tr2, err := Ensure(ctx, repo, data, "app", "111")
	if err != nil {
		t.Fatal(err)
	}
	if tr2.Path != tr.Path {
		t.Fatalf("reuse path %q vs %q", tr2.Path, tr.Path)
	}

	// Second thread gets a different tree
	trB, err := Ensure(ctx, repo, data, "app", "222")
	if err != nil {
		t.Fatal(err)
	}
	if trB.Path == tr.Path {
		t.Fatal("threads should not share worktree path")
	}

	if err := Remove(ctx, repo, tr.Path, tr.Branch); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(tr.Path); !os.IsNotExist(err) {
		t.Fatalf("path still exists: %v", err)
	}
	// Branch should be gone
	cmd := exec.Command("git", "-C", repo, "show-ref", "--verify", "--quiet", "refs/heads/"+tr.Branch)
	if cmd.Run() == nil {
		t.Fatal("branch still exists after remove")
	}
}

func TestEnsureNotARepo(t *testing.T) {
	dir := t.TempDir()
	_, err := Ensure(context.Background(), dir, t.TempDir(), "p", "1")
	if err == nil {
		t.Fatal("expected error for non-git dir")
	}
}

func TestSanitizePathSegment(t *testing.T) {
	if got := sanitizePathSegment("my app"); got != "my_app" {
		t.Fatalf("got %q", got)
	}
	got := sanitizePathSegment("../../../x")
	if strings.Contains(got, "/") || strings.Contains(got, string(filepath.Separator)) {
		t.Fatalf("unsafe %q", got)
	}
	if got == "." || got == ".." {
		t.Fatalf("unsafe %q", got)
	}
}

func initTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test",
			"GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=test",
			"GIT_COMMITTER_EMAIL=test@example.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init")
	// Default branch name varies; ensure we have a commit.
	if err := os.WriteFile(filepath.Join(dir, "README"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "README")
	run("commit", "-m", "init")
	return dir
}
