package grokrun

import (
	"strings"
	"testing"
)

func TestConsumeStream(t *testing.T) {
	raw := strings.Join([]string{
		`{"type":"thought","data":"Thinking"}`,
		`{"type":"text","data":"Hello"}`,
		`{"type":"text","data":" world"}`,
		`{"type":"end","sessionId":"sess-1","stopReason":"EndTurn"}`,
		``,
		`not-json`,
	}, "\n")

	var texts, thoughts []string
	text, sid, err := consumeStream(strings.NewReader(raw), func(d string) {
		texts = append(texts, d)
	}, func(d string) {
		thoughts = append(thoughts, d)
	})
	if text != "Hello world" {
		t.Fatalf("text=%q", text)
	}
	if sid != "sess-1" {
		t.Fatalf("session=%q", sid)
	}
	if len(texts) != 2 || texts[0] != "Hello" || texts[1] != " world" {
		t.Fatalf("texts=%v", texts)
	}
	if len(thoughts) != 1 || thoughts[0] != "Thinking" {
		t.Fatalf("thoughts=%v", thoughts)
	}
	if err == nil {
		t.Fatal("expected parse note for malformed line")
	}
}

func TestConsumeStreamErrorEvent(t *testing.T) {
	raw := `{"type":"error","message":"boom"}` + "\n"
	text, _, err := consumeStream(strings.NewReader(raw), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if text != "boom" {
		t.Fatalf("text=%q", text)
	}
}
