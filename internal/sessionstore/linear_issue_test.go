package sessionstore

import (
	"strings"
	"testing"
)

func TestParseLinearIssueRefs(t *testing.T) {
	got := ParseLinearIssueRefs("please fix ENG-123 and also https://linear.app/acme/issue/ENG-99/fix-auth")
	if len(got) != 2 {
		t.Fatalf("got %v", got)
	}
	byID := map[string]TrackedIssue{}
	for _, iss := range got {
		if !iss.IsLinear() {
			t.Fatalf("provider: %+v", iss)
		}
		byID[iss.Identifier] = iss
	}
	if byID["ENG-123"].EffectiveKeyword() != IssueKeywordFixes {
		t.Fatalf("ENG-123 keyword=%s", byID["ENG-123"].Keyword)
	}
	if !strings.Contains(byID["ENG-99"].URL, "ENG-99") {
		t.Fatalf("url=%s", byID["ENG-99"].URL)
	}
}

func TestParseLinearDiscordWrappedURL(t *testing.T) {
	got := ParseLinearIssueRefs("see <https://linear.app/ws/issue/ABC-7/slug>")
	if len(got) != 1 || got[0].Identifier != "ABC-7" {
		t.Fatalf("%+v", got)
	}
}

func TestSameIssueCrossProvider(t *testing.T) {
	gh := TrackedIssue{Number: 123}
	lin := TrackedIssue{Provider: ProviderLinear, Identifier: "ENG-123"}
	if sameIssue(gh, lin) {
		t.Fatal("must not match across providers")
	}
}

func TestUpsertLinearIssue(t *testing.T) {
	var e Entry
	e.UpsertIssue(TrackedIssue{Provider: ProviderLinear, Identifier: "eng-5", Keyword: IssueKeywordRefs})
	e.UpsertIssue(TrackedIssue{Provider: ProviderLinear, Identifier: "ENG-5", Keyword: IssueKeywordFixes, Title: "T", State: "Todo", URL: "https://linear.app/x/issue/ENG-5"})
	if len(e.Issues) != 1 {
		t.Fatalf("%+v", e.Issues)
	}
	if e.Issues[0].Identifier != "ENG-5" || e.Issues[0].EffectiveKeyword() != IssueKeywordFixes {
		t.Fatalf("%+v", e.Issues[0])
	}
	if e.Issues[0].Title != "T" {
		t.Fatalf("title=%s", e.Issues[0].Title)
	}
}

func TestRemoveLinearIssue(t *testing.T) {
	var e Entry
	e.UpsertIssue(TrackedIssue{Provider: ProviderLinear, Identifier: "ENG-1"})
	e.UpsertIssue(TrackedIssue{Number: 1})
	if !e.RemoveIssue("ENG-1") {
		t.Fatal("remove linear")
	}
	if len(e.Issues) != 1 || e.Issues[0].Number != 1 {
		t.Fatalf("%+v", e.Issues)
	}
}

func TestIssueTitlePrefixLinear(t *testing.T) {
	pref := IssueTitlePrefix([]TrackedIssue{
		{Provider: ProviderLinear, Identifier: "ENG-9"},
		{Number: 3},
	})
	if !strings.Contains(pref, "ENG-9") || !strings.Contains(pref, "#3") {
		t.Fatalf("%q", pref)
	}
}

func TestLinearPRBodyLine(t *testing.T) {
	iss := TrackedIssue{Provider: ProviderLinear, Identifier: "ENG-2", Keyword: IssueKeywordFixes}
	if got := iss.PRBodyLine(); got != "Fixes ENG-2" {
		t.Fatalf("%q", got)
	}
}
