package grokrun

import "testing"

func TestExtractVerifyCommandsText(t *testing.T) {
	raw := "Here are some checks:\n\n```\nunit | go test ./...\nlint | make lint | 300000\n```\n\nHope that helps."
	got := ExtractVerifyCommandsText(raw)
	want := "unit | go test ./...\nlint | make lint | 300000"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestExtractVerifyCommandsTextPlainAndLists(t *testing.T) {
	raw := `
- unit | go test ./...
1. lint | make lint
• fmt: gofmt -l .
Note: ignore this
https://example.com/foo
`
	got := ExtractVerifyCommandsText(raw)
	want := "unit | go test ./...\nlint | make lint\nfmt: gofmt -l ."
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestExtractVerifyCommandsTextEmpty(t *testing.T) {
	if ExtractVerifyCommandsText("") != "" {
		t.Fatal("empty")
	}
	if ExtractVerifyCommandsText("No commands found.") != "" {
		t.Fatal("prose only")
	}
}
