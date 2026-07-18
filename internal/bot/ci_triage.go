package bot

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/acoshift/grok-discord/internal/ghpr"
	"github.com/acoshift/grok-discord/internal/sessionstore"
)

const ciLogSnippetRunes = 1500

// maybeHandleCIFailure posts a debounced CI digest and optionally queues /fix-ci.
// Call after PR info is applied for an open PR while the thread is idle.
func (b *Bot) maybeHandleCIFailure(s *discordgo.Session, threadID string, info ghpr.Info) {
	if s == nil || threadID == "" || info.Number <= 0 {
		return
	}
	if ghpr.IsTerminal(info.State) {
		return
	}
	if b.isThreadBusy(threadID) {
		return
	}

	e, ok := b.sessions.Get(threadID)
	if !ok {
		return
	}
	repoDir := prRepoDir(e)
	if repoDir == "" {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	checks, err := ghpr.ListChecks(ctx, repoDir, info.Number)
	if err != nil {
		log.Printf("ci-triage: list checks thread=%s pr=%d: %v", threadID, info.Number, err)
		// Fall back to rollup string from PR card.
		if !strings.Contains(info.Checks, "✗") {
			return
		}
		checks = nil
	}
	failed := ghpr.FailedChecks(checks)
	if len(failed) == 0 && !strings.Contains(info.Checks, "✗") {
		return
	}

	headSHA := strings.TrimSpace(info.HeadSHA)
	if headSHA == "" {
		headSHA = strings.TrimSpace(e.PRHeadSHA)
	}

	// Debounce: one digest per head SHA.
	if headSHA != "" && headSHA == e.CINotifiedSHA {
		return
	}

	digest := ghpr.FormatCIDigest(info.Number, headSHA, failed)
	snippet := ""
	branch := e.WorktreeBranch
	if branch == "" {
		branch = info.HeadRef
	}
	if branch != "" {
		snippet = ghpr.FailedLogSnippet(ctx, repoDir, branch, headSHA, ciLogSnippetRunes)
	}
	if snippet != "" {
		digest += "\n\n**log (tail):**\n```\n" + snippet + "\n```"
		if len([]rune(digest)) > 1900 {
			digest = truncateRunes(digest, 1900)
		}
	}

	if _, err := discordSend(s, threadID, digest); err != nil {
		log.Printf("ci-triage: digest thread=%s: %v", threadID, err)
		return
	}
	log.Printf("ci-triage: digest posted thread=%s pr=%d sha=%s fails=%d",
		threadID, info.Number, shortSHA(headSHA), len(failed))

	if _, _, pErr := b.sessions.Patch(threadID, func(ent *sessionstore.Entry) {
		if headSHA != "" {
			ent.CINotifiedSHA = headSHA
		}
	}); pErr != nil {
		log.Printf("ci-triage: patch notified sha thread=%s: %v", threadID, pErr)
	}

	if !b.cfg.AutoFixCIEnabled() {
		return
	}
	maxAttempts := b.cfg.AutoFixCIMaxAttempts()
	if e.CIAutoFixCount >= maxAttempts {
		log.Printf("ci-triage: auto-fix cap reached thread=%s count=%d max=%d",
			threadID, e.CIAutoFixCount, maxAttempts)
		if _, sendErr := discordSend(s, threadID, fmt.Sprintf(
			"Auto CI fix disabled for this thread (already tried %d/%d). Use `@Grok /fix-ci` manually.",
			e.CIAutoFixCount, maxAttempts,
		)); sendErr != nil {
			log.Printf("ci-triage: auto-cap notice: %v", sendErr)
		}
		return
	}
	if headSHA != "" && headSHA == e.CIAutoFixSHA {
		return
	}

	prompt := buildFixCIPrompt(info.Number, headSHA, branch, failed, snippet)
	if err := b.queueSystemTask(s, threadID, prompt, "auto-fix-ci"); err != nil {
		log.Printf("ci-triage: auto queue thread=%s: %v", threadID, err)
		if _, sendErr := discordSend(s, threadID, "Could not queue auto CI fix: "+err.Error()); sendErr != nil {
			log.Printf("ci-triage: auto queue notice: %v", sendErr)
		}
		return
	}
	if _, _, pErr := b.sessions.Patch(threadID, func(ent *sessionstore.Entry) {
		ent.CIAutoFixCount++
		if headSHA != "" {
			ent.CIAutoFixSHA = headSHA
		}
	}); pErr != nil {
		log.Printf("ci-triage: patch auto-fix count thread=%s: %v", threadID, pErr)
	}
	if _, sendErr := discordSend(s, threadID, fmt.Sprintf(
		"Auto-queued CI fix (%d/%d for this thread)…", e.CIAutoFixCount+1, maxAttempts,
	)); sendErr != nil {
		log.Printf("ci-triage: auto-queued notice: %v", sendErr)
	}
}

func (b *Bot) handleFixCI(s *discordgo.Session, m *discordgo.MessageCreate) {
	if !isThread(s, m.ChannelID) {
		if _, err := s.ChannelMessageSendReply(m.ChannelID, "Use `@Grok /fix-ci` inside a Grok thread with an open PR.", ref(m)); err != nil {
			log.Printf("error: reply fix-ci-not-thread: %v", err)
		}
		return
	}
	threadID := m.ChannelID
	e, ok := b.sessions.Get(threadID)
	if !ok || (e.PRNumber <= 0 && e.PRURL == "") {
		if _, err := s.ChannelMessageSendReply(threadID, "No PR linked to this thread yet. Run a task that opens a PR first.", ref(m)); err != nil {
			log.Printf("error: reply fix-ci-no-pr: %v", err)
		}
		return
	}

	repoDir := prRepoDir(e)
	if repoDir == "" {
		if _, err := s.ChannelMessageSendReply(threadID, "No git worktree/repo for this thread.", ref(m)); err != nil {
			log.Printf("error: reply fix-ci-no-repo: %v", err)
		}
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	prNum := e.PRNumber
	headSHA := e.PRHeadSHA
	// Refresh PR snapshot when possible.
	if info, err := b.resolvePRInfo(ctx, repoDir, e, ""); err == nil {
		_ = b.applyPRInfo(s, threadID, info)
		prNum = info.Number
		if info.HeadSHA != "" {
			headSHA = info.HeadSHA
		}
	}

	var failed []ghpr.Check
	if prNum > 0 {
		if checks, err := ghpr.ListChecks(ctx, repoDir, prNum); err == nil {
			failed = ghpr.FailedChecks(checks)
		}
	}
	if len(failed) == 0 {
		if _, err := s.ChannelMessageSendReply(threadID, "No failing checks right now (or checks still pending). Nothing to fix.", ref(m)); err != nil {
			log.Printf("error: reply fix-ci-clean: %v", err)
		}
		return
	}

	branch := e.WorktreeBranch
	snippet := ""
	if branch != "" {
		snippet = ghpr.FailedLogSnippet(ctx, repoDir, branch, headSHA, ciLogSnippetRunes)
	}
	prompt := buildFixCIPrompt(prNum, headSHA, branch, failed, snippet)

	// Manual fix-ci goes through normal task path so queue/allowlist semantics match.
	go b.handleTask(s, m, Parsed{Kind: KindTask, Prompt: prompt})
}

func buildFixCIPrompt(prNumber int, headSHA, branch string, failed []ghpr.Check, logSnippet string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "CI failed on pull request #%d", prNumber)
	if headSHA != "" {
		fmt.Fprintf(&b, " (head %s)", shortSHA(headSHA))
	}
	b.WriteString(".\n")
	if branch != "" {
		fmt.Fprintf(&b, "Stay on branch %s (this thread's worktree). Do not switch to main/master.\n", branch)
	}
	b.WriteString("Failed checks:\n")
	if len(failed) == 0 {
		b.WriteString("- (see gh pr checks)\n")
	} else {
		for _, c := range failed {
			name := strings.TrimSpace(c.Name)
			if name == "" {
				name = "(unnamed)"
			}
			if c.Link != "" {
				fmt.Fprintf(&b, "- %s (%s)\n", name, c.Link)
			} else {
				fmt.Fprintf(&b, "- %s\n", name)
			}
		}
	}
	b.WriteString("\nTasks:\n")
	b.WriteString("1. Inspect failures with `gh pr checks` and `gh run view --log-failed` as needed.\n")
	b.WriteString("2. Apply a minimal fix for the CI failures only.\n")
	b.WriteString("3. Run the relevant tests/build commands locally when practical.\n")
	b.WriteString("4. Commit, push to this branch, and update the existing PR (do not open a duplicate; do not merge).\n")
	b.WriteString("5. Summarize what failed and what you changed.\n")
	if logSnippet != "" {
		b.WriteString("\nFailed log tail (may be truncated):\n```\n")
		b.WriteString(logSnippet)
		b.WriteString("\n```\n")
	}
	return strings.TrimSpace(b.String())
}

// queueSystemTask enqueues a Grok task on an existing thread without a user message.
func (b *Bot) queueSystemTask(s *discordgo.Session, threadID, prompt, label string) error {
	if s == nil || threadID == "" || strings.TrimSpace(prompt) == "" {
		return fmt.Errorf("missing session, thread, or prompt")
	}
	e, ok := b.sessions.Get(threadID)
	if !ok {
		return fmt.Errorf("no session for thread")
	}
	projName := e.Project
	if projName == "" {
		return fmt.Errorf("session has no project")
	}
	cwd, ok := b.cfg.ProjectPath(projName)
	if !ok || cwd == "" {
		return fmt.Errorf("project %q not in config", projName)
	}
	// Prefer main repo path from session when set.
	if e.MainCwd != "" {
		cwd = e.MainCwd
	}

	m := &discordgo.MessageCreate{
		Message: &discordgo.Message{
			ID:        fmt.Sprintf("%s-%d", label, time.Now().UnixNano()),
			ChannelID: threadID,
			Author:    &discordgo.User{ID: "0", Username: label},
			Content:   prompt,
		},
	}
	parsed := Parsed{Kind: KindTask, Prompt: prompt}
	item := taskItem{s: s, m: m, parsed: parsed, proj: projectRef{Name: projName, Cwd: cwd}, threadID: threadID}

	ctx, cancel := context.WithCancel(context.Background())
	job := &runJob{cancel: cancel, start: time.Now(), project: projName}
	claimed, queuePos, err := b.claimOrEnqueue(threadID, job, item)
	if err != nil {
		cancel()
		return err
	}
	if !claimed {
		cancel()
		log.Printf("ci-triage: system task queued pos=%d thread=%s label=%s", queuePos, threadID, label)
		return nil
	}
	go b.drainTaskQueue(ctx, cancel, item, job)
	return nil
}

// drainTaskQueue runs the active task and any follow-ups (same loop as handleTask after claim).
func (b *Bot) drainTaskQueue(ctx context.Context, cancel context.CancelFunc, item taskItem, job *runJob) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("error: panic in drainTaskQueue thread=%s: %v", item.threadID, r)
		}
	}()
	for {
		b.executeTask(ctx, item, job)
		cancel()

		next, ok := b.finishRun(item.threadID)
		if !ok {
			b.tryCleanupTerminalPR(item.threadID)
			return
		}
		nextCtx, nextCancel := context.WithCancel(context.Background())
		nextJob := &runJob{cancel: nextCancel, start: time.Now(), project: next.proj.Name}
		b.replaceJob(next.threadID, nextJob)
		log.Printf("task: draining queue thread=%s nextMsg=%s remaining=%d",
			next.threadID, next.m.ID, b.queueLen(next.threadID))
		if _, sendErr := next.s.ChannelMessageSend(next.threadID, "Starting queued follow-up…"); sendErr != nil {
			log.Printf("error: reply queue-start: %v", sendErr)
		}
		item = next
		job = nextJob
		ctx = nextCtx
		cancel = nextCancel
	}
}

func shortSHA(sha string) string {
	sha = strings.TrimSpace(sha)
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}
