package bot

import (
	"strings"
	"testing"

	"github.com/acoshift/grok-discord/internal/sessionstore"
)

func TestIssueBindingPrompt(t *testing.T) {
	p := issueBindingPrompt(nil)
	if p != "" {
		t.Fatalf("empty: %q", p)
	}
	p = issueBindingPrompt([]sessionstore.TrackedIssue{
		{Number: 42, Keyword: sessionstore.IssueKeywordFixes, Owner: "o", Repo: "r"},
		{Number: 7, Keyword: sessionstore.IssueKeywordRefs},
	})
	for _, want := range []string{
		"Linked GitHub issues",
		"Fixes o/r#42",
		"Refs #7",
		"Prefix the PR title",
		"#42 #7",
	} {
		if !strings.Contains(p, want) {
			t.Fatalf("missing %q in:\n%s", want, p)
		}
	}
}

func TestParseLinkArg(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"/link", ""},
		{"link", ""},
		{"/link #42", "#42"},
		{"/link fix #42", "fix #42"},
		{"/unlink #9", "unlink #9"},
		{"unlink 9", "unlink 9"},
	}
	for _, tc := range cases {
		if got := parseLinkArg(tc.in); got != tc.want {
			t.Fatalf("%q: got %q want %q", tc.in, got, tc.want)
		}
	}
}

func TestSplitLinkKeyword(t *testing.T) {
	kw, rest := splitLinkKeyword("fix #42")
	if kw != sessionstore.IssueKeywordFixes || rest != "#42" {
		t.Fatalf("got %q %q", kw, rest)
	}
	kw, rest = splitLinkKeyword("refs o/r#3")
	if kw != sessionstore.IssueKeywordRefs || rest != "o/r#3" {
		t.Fatalf("got %q %q", kw, rest)
	}
	kw, rest = splitLinkKeyword("#99")
	if kw != "" || rest != "#99" {
		t.Fatalf("got %q %q", kw, rest)
	}
}

func TestPrefixThreadTitleWithIssues(t *testing.T) {
	issues := []sessionstore.TrackedIssue{{Number: 42}}
	got := prefixThreadTitleWithIssues("fix payment timeout", issues)
	if got != "#42 fix payment timeout" {
		t.Fatalf("got %q", got)
	}
	// Idempotent
	got = prefixThreadTitleWithIssues("#42 fix payment timeout", issues)
	if got != "#42 fix payment timeout" {
		t.Fatalf("double: %q", got)
	}
}

func TestPreserveIssueFields(t *testing.T) {
	prev := sessionstore.Entry{
		Issues: []sessionstore.TrackedIssue{{Number: 5, Keyword: sessionstore.IssueKeywordFixes}},
	}
	next := sessionstore.Entry{SessionID: "s"}
	preserveIssueFields(&next, prev)
	if len(next.Issues) != 1 || next.Issues[0].Number != 5 {
		t.Fatalf("got %+v", next.Issues)
	}
	// Do not clobber.
	next2 := sessionstore.Entry{Issues: []sessionstore.TrackedIssue{{Number: 9}}}
	preserveIssueFields(&next2, prev)
	if next2.Issues[0].Number != 9 {
		t.Fatalf("clobber: %+v", next2.Issues)
	}
}

func TestBindIssuesFromText(t *testing.T) {
	b := testBot(t)
	if err := b.sessions.Set("t1", sessionstore.Entry{Project: "app"}); err != nil {
		t.Fatal(err)
	}
	bound := b.bindIssuesFromText("t1", "please fix #88 in auth", "acoshift", "grok-discord")
	if len(bound) != 1 {
		t.Fatalf("bound=%v", bound)
	}
	e, _ := b.sessions.Get("t1")
	if !e.HasIssues() || e.Issues[0].Number != 88 {
		t.Fatalf("%+v", e.Issues)
	}
	if e.Issues[0].EffectiveKeyword() != sessionstore.IssueKeywordFixes {
		t.Fatalf("keyword=%s", e.Issues[0].Keyword)
	}
	if e.Issues[0].Owner != "acoshift" {
		t.Fatalf("owner=%s", e.Issues[0].Owner)
	}
}
