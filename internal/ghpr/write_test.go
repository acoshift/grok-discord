package ghpr

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestCommentIssueUsesBodyFile(t *testing.T) {
	var saw []string
	var bodyPath string
	run := func(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
		saw = append([]string{name}, args...)
		for i, a := range args {
			if a == "--body-file" && i+1 < len(args) {
				bodyPath = args[i+1]
				b, err := os.ReadFile(bodyPath)
				if err != nil {
					t.Fatal(err)
				}
				if string(b) != "hello #1" {
					t.Fatalf("body file=%q", b)
				}
			}
		}
		return []byte("ok"), nil
	}
	if err := CommentIssueWith(context.Background(), run, "/repo", "o", "r", 3, "hello #1"); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(saw, " ")
	if !strings.Contains(joined, "issue comment 3") || !strings.Contains(joined, "--body-file") {
		t.Fatalf("args=%v", saw)
	}
	if !strings.Contains(joined, "--repo o/r") {
		t.Fatalf("missing --repo: %v", saw)
	}
	if bodyPath == "" {
		t.Fatal("no body file")
	}
	// temp file cleaned up
	if _, err := os.Stat(bodyPath); !os.IsNotExist(err) {
		t.Fatalf("body file should be removed: %v", err)
	}
}

func TestCommentPRAndClose(t *testing.T) {
	var last []string
	run := func(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
		last = append([]string{name}, args...)
		return nil, nil
	}
	if err := CommentPRWith(context.Background(), run, "/r", "a", "b", 9, "note"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(last, " "), "pr comment 9") {
		t.Fatalf("%v", last)
	}
	if err := ClosePRWith(context.Background(), run, "/r", "a", "b", 9); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(last, " "), "pr close 9") || !strings.Contains(strings.Join(last, " "), "--repo a/b") {
		t.Fatalf("%v", last)
	}
}

func TestCheckMergePreflight(t *testing.T) {
	ok := CheckMergePreflight("OPEN", "MERGEABLE", "✓ 1", false)
	if !ok.Allow {
		t.Fatalf("%+v", ok)
	}
	if CheckMergePreflight("MERGED", "MERGEABLE", "✓ 1", false).Allow {
		t.Fatal("merged should refuse")
	}
	if CheckMergePreflight("OPEN", "CONFLICTING", "✓ 1", false).Allow {
		t.Fatal("conflict should refuse")
	}
	fail := CheckMergePreflight("OPEN", "MERGEABLE", "✓ 1 · ✗ 1", false)
	if fail.Allow {
		t.Fatal("failing checks should refuse")
	}
	anyway := CheckMergePreflight("OPEN", "MERGEABLE", "✓ 1 · ✗ 1", true)
	if !anyway.Allow {
		t.Fatal("attempt anyway should allow")
	}
}

func TestMergePRDefaultSquashNoAdmin(t *testing.T) {
	var last []string
	run := func(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
		last = append([]string{name}, args...)
		for _, a := range args {
			if a == "--admin" {
				t.Fatal("must not pass --admin")
			}
		}
		return nil, nil
	}
	if err := MergePRWith(context.Background(), run, "/r", "o", "r", 4, MergeOpts{}); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(last, " ")
	if !strings.Contains(joined, "pr merge 4") || !strings.Contains(joined, "--squash") {
		t.Fatalf("%v", last)
	}
	if !strings.Contains(joined, "--repo o/r") {
		t.Fatalf("%v", last)
	}
	if err := MergePRWith(context.Background(), run, "/r", "o", "r", 4, MergeOpts{Method: MergeMerge}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(last, " "), "--merge") {
		t.Fatalf("%v", last)
	}
}

func TestNormalizeMergeMethod(t *testing.T) {
	if NormalizeMergeMethod("") != MergeSquash {
		t.Fatal("default")
	}
	if NormalizeMergeMethod("MERGE") != MergeMerge {
		t.Fatal("merge")
	}
}

func TestEmptyCommentRejected(t *testing.T) {
	run := func(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
		t.Fatal("should not run")
		return nil, nil
	}
	if err := CommentIssueWith(context.Background(), run, "/r", "o", "r", 1, "  "); err == nil {
		t.Fatal("expected error")
	}
}
