package bot

import (
	"fmt"
	"strings"
	"time"

	"github.com/acoshift/grokwork/internal/config"
	"github.com/acoshift/grokwork/internal/sessionstore"
)

// Case action errors (web + tests).
var (
	ErrNotACase       = fmt.Errorf("not a case session")
	ErrCaseClosed     = fmt.Errorf("case is closed")
	ErrCaseForbidden  = fmt.Errorf("not allowed for this case action")
	ErrCaseNoSession  = fmt.Errorf("unknown session")
	ErrCaseEmptyTitle = fmt.Errorf("customer update empty after sanitizer")
)

// EscalateCase moves Mode=case → Phase=fixing (K17: Mode stays case).
// Caps must be checked by the caller (FileEscalation / builder-class).
func (b *Bot) EscalateCase(threadID, actorID, note string) error {
	if b == nil || b.sessions == nil {
		return fmt.Errorf("bot unavailable")
	}
	threadID = strings.TrimSpace(threadID)
	e, ok := b.sessions.Get(threadID)
	if !ok {
		return ErrCaseNoSession
	}
	if !e.IsCase() {
		return ErrNotACase
	}
	if e.IsCaseClosed() {
		return ErrCaseClosed
	}
	note = strings.TrimSpace(note)
	now := time.Now().UTC().Format(time.RFC3339)
	_, _, err := b.sessions.Patch(threadID, func(ent *sessionstore.Entry) {
		ent.Mode = ModeCase
		ent.Phase = sessionstore.PhaseFixing
		ent.EscalatedAt = now
		ent.EscalatedBy = strings.TrimSpace(actorID)
		if note != "" {
			if ent.Dossier == nil {
				ent.Dossier = &sessionstore.Dossier{}
			}
			ent.Dossier.NextActions = append(ent.Dossier.NextActions, "Escalate note: "+note)
		}
		if ent.Label == sessionstore.LabelBlocked || ent.Label == sessionstore.LabelOpen {
			ent.Label = sessionstore.LabelInProgress
		}
		_ = sessionstore.ClampCaseFields(ent)
	})
	return err
}

// AnswerCase moves Mode=case → Phase=answered; optional note becomes customer update.
func (b *Bot) AnswerCase(threadID, actorID, note string) error {
	if b == nil || b.sessions == nil {
		return fmt.Errorf("bot unavailable")
	}
	threadID = strings.TrimSpace(threadID)
	e, ok := b.sessions.Get(threadID)
	if !ok {
		return ErrCaseNoSession
	}
	if !e.IsCase() {
		return ErrNotACase
	}
	if e.IsCaseClosed() {
		return ErrCaseClosed
	}
	note = strings.TrimSpace(note)
	_, _, err := b.sessions.Patch(threadID, func(ent *sessionstore.Entry) {
		ent.Mode = ModeCase
		ent.Phase = sessionstore.PhaseAnswered
		ent.Label = sessionstore.LabelBlocked
		if note != "" {
			clean, _ := SanitizeCustomerUpdate(note)
			if clean != "" {
				ent.CustomerUpdate = clean
			}
		}
		_ = sessionstore.ClampCaseFields(ent)
	})
	return err
}

// CloseCase freezes the case (Phase=closed). Caller enforces ownership.
func (b *Bot) CloseCase(threadID, actorID, resolution, note string) error {
	if b == nil || b.sessions == nil {
		return fmt.Errorf("bot unavailable")
	}
	threadID = strings.TrimSpace(threadID)
	e, ok := b.sessions.Get(threadID)
	if !ok {
		return ErrCaseNoSession
	}
	if !e.IsCase() {
		return ErrNotACase
	}
	if e.IsCaseClosed() {
		return ErrCaseClosed
	}
	res := strings.ToLower(strings.TrimSpace(resolution))
	if res == "" {
		res = "answered"
	}
	switch res {
	case "answered", "fixed", "duplicate", "wontfix", "escalated_external":
	default:
		// treat free text as answered + note
		if note == "" {
			note = resolution
		}
		res = "answered"
	}
	label := sessionstore.LabelDone
	switch res {
	case "wontfix", "escalated_external":
		label = sessionstore.LabelAbandoned
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, _, err := b.sessions.Patch(threadID, func(ent *sessionstore.Entry) {
		ent.Mode = ModeCase
		ent.Phase = sessionstore.PhaseClosed
		ent.Resolution = res
		ent.ResolutionNote = strings.TrimSpace(note)
		ent.ResolvedAt = now
		ent.ResolvedBy = strings.TrimSpace(actorID)
		ent.Label = label
		_ = sessionstore.ClampCaseFields(ent)
	})
	return err
}

// SetCaseCustomerUpdate sanitizes and stores customer-facing text.
// Returns cleaned text and redaction hits.
func (b *Bot) SetCaseCustomerUpdate(threadID, text string) (clean string, hits []string, err error) {
	if b == nil || b.sessions == nil {
		return "", nil, fmt.Errorf("bot unavailable")
	}
	threadID = strings.TrimSpace(threadID)
	e, ok := b.sessions.Get(threadID)
	if !ok {
		return "", nil, ErrCaseNoSession
	}
	if !e.IsCase() {
		return "", nil, ErrNotACase
	}
	if e.IsCaseClosed() {
		return "", nil, ErrCaseClosed
	}
	clean, hits = SanitizeCustomerUpdate(text)
	if clean == "" {
		return "", hits, ErrCaseEmptyTitle
	}
	_, _, err = b.sessions.Patch(threadID, func(ent *sessionstore.Entry) {
		ent.CustomerUpdate = clean
		_ = sessionstore.ClampCaseFields(ent)
	})
	return clean, hits, err
}

// CanEscalateCaseCaps is the shared escalate gate (Discord + web).
func CanEscalateCaseCaps(caps config.Capabilities) bool {
	return canEscalateCase(caps)
}

// CanDraftCaseCaps is the answer / customer-update gate.
func CanDraftCaseCaps(caps config.Capabilities) bool {
	return caps.DraftCustomerReply || canEscalateCase(caps)
}
