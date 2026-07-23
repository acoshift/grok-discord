package bot

import (
	"path/filepath"
	"testing"

	"github.com/acoshift/grokwork/internal/config"
	"github.com/acoshift/grokwork/internal/sessionstore"
)

func TestCaseActionsLifecycle(t *testing.T) {
	dir := t.TempDir()
	store, err := sessionstore.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Projects: config.PathProjects(map[string]string{"app": filepath.Join(dir, "app")})}
	b := New(cfg, store, nil)
	tid := "t-case-1"
	if err := store.Set(tid, sessionstore.Entry{
		Project: "app", Mode: ModeCase, Phase: sessionstore.PhaseIntake,
		CustomerTitle: "Checkout fails", Severity: "high", OwnerID: "u1",
	}); err != nil {
		t.Fatal(err)
	}
	if err := b.EscalateCase(tid, "u-eng", "stack in thread"); err != nil {
		t.Fatal(err)
	}
	e, _ := store.Get(tid)
	if e.Phase != sessionstore.PhaseFixing || e.Mode != ModeCase || e.EscalatedBy != "u-eng" {
		t.Fatalf("after escalate: %+v", e)
	}
	// reopen path via answer not from fixing without close - answer from fixing ok
	if err := b.AnswerCase(tid, "u1", "Please update the app"); err != nil {
		t.Fatal(err)
	}
	e, _ = store.Get(tid)
	if e.Phase != sessionstore.PhaseAnswered || e.CustomerUpdate == "" {
		t.Fatalf("after answer: %+v", e)
	}
	if _, _, err := b.SetCaseCustomerUpdate(tid, "Safe reply for customer"); err != nil {
		t.Fatal(err)
	}
	if err := b.CloseCase(tid, "u1", "answered", ""); err != nil {
		t.Fatal(err)
	}
	e, _ = store.Get(tid)
	if !e.IsCaseClosed() || e.Resolution != "answered" {
		t.Fatalf("after close: %+v", e)
	}
	if err := b.EscalateCase(tid, "u-eng", ""); err != ErrCaseClosed {
		t.Fatalf("want ErrCaseClosed, got %v", err)
	}
}

func TestCanEscalateCaseCaps(t *testing.T) {
	if CanEscalateCaseCaps(config.Capabilities{Investigate: true}) {
		t.Fatal("investigate alone cannot escalate")
	}
	if !CanEscalateCaseCaps(config.Capabilities{FileEscalation: true}) {
		t.Fatal("fileEscalation should escalate")
	}
	if !CanDraftCaseCaps(config.Capabilities{DraftCustomerReply: true}) {
		t.Fatal("draft should draft")
	}
}
