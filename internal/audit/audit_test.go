package audit

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAppendAndReadDay(t *testing.T) {
	dir := t.TempDir()
	log, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	fixed := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	log.now = func() time.Time { return fixed }

	if err := log.Append(Event{
		Action: ActionConfigSettings,
		Actor:  "12345",
		Role:   "admin",
		OK:     true,
		Detail: map[string]any{"section": "worktree"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := log.Append(Event{
		Action: ActionLoginFail,
		Actor:  "stranger",
		OK:     false,
		Error:  "not authorized",
	}); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(log.Dir(), "2026-07-20.jsonl")
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("mode=%o want 0600", st.Mode().Perm())
	}

	evs, err := log.ReadDay(fixed)
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 2 {
		t.Fatalf("len=%d", len(evs))
	}
	if evs[0].Action != ActionConfigSettings || evs[0].Actor != "12345" || !evs[0].OK {
		t.Fatalf("first=%+v", evs[0])
	}
	if evs[0].Time.IsZero() {
		t.Fatal("missing time")
	}
	if evs[1].Action != ActionLoginFail || evs[1].OK {
		t.Fatalf("second=%+v", evs[1])
	}
}

func TestAppendEmptyActorBecomesAnonymous(t *testing.T) {
	log, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := log.Append(Event{Action: ActionWorktreePrune, OK: true}); err != nil {
		t.Fatal(err)
	}
	evs, err := log.ReadDay(time.Now())
	if err != nil || len(evs) != 1 {
		t.Fatalf("evs=%v err=%v", evs, err)
	}
	if evs[0].Actor != ActorAnonymous {
		t.Fatalf("actor=%q", evs[0].Actor)
	}
}

func TestNilLoggerNoop(t *testing.T) {
	var log *Logger
	if err := log.Append(Event{Action: "x"}); err != nil {
		t.Fatal(err)
	}
}

func TestEmptyActionRejected(t *testing.T) {
	log, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := log.Append(Event{}); err == nil {
		t.Fatal("expected error")
	}
}
