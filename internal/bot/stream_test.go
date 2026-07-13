package bot

import "testing"

func TestStreamPreview(t *testing.T) {
	if got := streamPreview("hi", 100); got != "hi" {
		t.Fatalf("short=%q", got)
	}
	long := "aaaaaaaaaa\nbbbbbbbbbb\ncccccccccc"
	got := streamPreview(long, 15)
	if len(got) > 16 { // budget + ellipsis
		t.Fatalf("too long: %q", got)
	}
	if got == "" {
		t.Fatal("empty")
	}
}

func TestThoughtTracker(t *testing.T) {
	var tr thoughtTracker
	tr.OnDelta("Analyzing the")
	tr.OnDelta(" codebase structure")
	if tr.Latest() == "" {
		t.Fatal("expected latest")
	}
}
