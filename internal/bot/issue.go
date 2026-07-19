package bot

import (
	"fmt"
	"log"
	"strings"

	"github.com/bwmarrin/discordgo"

	"github.com/acoshift/grok-discord/internal/sessionstore"
)

// preserveIssueFields copies bound issues when session Set overwrites the entry.
func preserveIssueFields(next *sessionstore.Entry, prev sessionstore.Entry) {
	if next == nil {
		return
	}
	if len(next.Issues) == 0 && len(prev.Issues) > 0 {
		next.Issues = append([]sessionstore.TrackedIssue(nil), prev.Issues...)
	}
}

// issueBindingPrompt injects linked-issue contract for Grok (PR body + title).
func issueBindingPrompt(issues []sessionstore.TrackedIssue) string {
	if len(issues) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Linked GitHub issues for this Discord thread:\n")
	for _, iss := range issues {
		ref := iss.DisplayRef()
		b.WriteString(fmt.Sprintf("- %s (%s)", ref, iss.EffectiveKeyword()))
		if u := strings.TrimSpace(iss.URL); u != "" {
			b.WriteString(" · " + u)
		}
		b.WriteString("\n")
	}
	b.WriteString("When you open or update a pull request you MUST:\n")
	b.WriteString("1. Include these exact lines in the PR body (GitHub closing keywords):\n")
	for _, iss := range issues {
		if line := iss.PRBodyLine(); line != "" {
			b.WriteString("   " + line + "\n")
		}
	}
	b.WriteString("2. Prefix the PR title with the issue numbers if missing (e.g. \"")
	b.WriteString(strings.TrimSpace(sessionstore.IssueTitlePrefix(issues)))
	b.WriteString(" short summary\").\n")
	b.WriteString("Do not invent other issue numbers. Do not merge the PR.\n\n")
	return b.String()
}

// bindIssuesFromText parses issue refs from text and upserts them onto the session.
// Returns newly-bound or updated issues (may be empty). defaultOwner/Repo fill bare #N.
func (b *Bot) bindIssuesFromText(threadID, text, defaultOwner, defaultRepo string) []sessionstore.TrackedIssue {
	if b == nil || b.sessions == nil || threadID == "" {
		return nil
	}
	parsed := sessionstore.ParseIssueRefs(text)
	if len(parsed) == 0 {
		return nil
	}
	sessionstore.FillIssueOwnerRepo(parsed, defaultOwner, defaultRepo)

	var bound []sessionstore.TrackedIssue
	_, ok, err := b.sessions.Patch(threadID, func(ent *sessionstore.Entry) {
		for _, iss := range parsed {
			ent.UpsertIssue(iss)
		}
		bound = append([]sessionstore.TrackedIssue(nil), ent.Issues...)
	})
	if err != nil {
		log.Printf("warn: bind issues thread=%s: %v", threadID, err)
		return nil
	}
	if ok {
		return bound
	}
	// No session yet: create shell with issues.
	e := sessionstore.Entry{Issues: parsed}
	if err := b.sessions.Set(threadID, e); err != nil {
		log.Printf("warn: bind issues create thread=%s: %v", threadID, err)
		return nil
	}
	return parsed
}

// defaultIssueRepo returns owner, repo for bare issue numbers from session PRs.
func defaultIssueRepo(e sessionstore.Entry) (owner, repo string) {
	e.NormalizePRs()
	if p, ok := e.PrimaryPR(); ok && p.Owner != "" && p.Repo != "" {
		return p.Owner, p.Repo
	}
	for _, p := range e.PRs {
		if p.Owner != "" && p.Repo != "" {
			return p.Owner, p.Repo
		}
	}
	for _, iss := range e.Issues {
		if iss.Owner != "" && iss.Repo != "" {
			return iss.Owner, iss.Repo
		}
	}
	return "", ""
}

func (b *Bot) handleLink(s *discordgo.Session, m *discordgo.MessageCreate, parsed Parsed) {
	if !isThread(s, m.ChannelID) {
		if _, err := s.ChannelMessageSendReply(m.ChannelID, "Use `@Grok /link` inside a Grok thread.", ref(m)); err != nil {
			log.Printf("error: reply link-not-thread: %v", err)
		}
		return
	}
	threadID := m.ChannelID
	arg := parseLinkArg(parsed.Prompt)

	e, ok := b.sessions.Get(threadID)
	if !ok {
		parentID := parentChannelID(s, threadID)
		projName := ""
		if p, err := b.resolveProject(parentID); err == nil {
			projName = p.Name
		}
		e = sessionstore.Entry{Project: projName}
		if m.Author != nil {
			ensureSessionOwner(&e, m.Author.ID, m.Author.String())
		}
	}

	switch {
	case arg == "" || arg == "list" || arg == "show":
		msg := formatLinkStatus(e)
		if _, err := s.ChannelMessageSendReply(threadID, msg, ref(m)); err != nil {
			log.Printf("error: reply link-status: %v", err)
		}
		return
	case arg == "help" || arg == "?":
		if _, err := s.ChannelMessageSendReply(threadID, linkHelpText(), ref(m)); err != nil {
			log.Printf("error: reply link-help: %v", err)
		}
		return
	case arg == "clear" || arg == "none" || arg == "reset":
		e.ClearIssues()
		if m.Author != nil {
			ensureSessionOwner(&e, m.Author.ID, m.Author.String())
		}
		if e.Project == "" {
			parentID := parentChannelID(s, threadID)
			if p, err := b.resolveProject(parentID); err == nil {
				e.Project = p.Name
			}
		}
		if err := b.sessions.Set(threadID, e); err != nil {
			if _, sendErr := s.ChannelMessageSendReply(threadID, "Could not clear issues: "+err.Error(), ref(m)); sendErr != nil {
				log.Printf("error: reply link-clear: %v", sendErr)
			}
			return
		}
		if _, err := s.ChannelMessageSendReply(threadID, "Cleared linked issues for this thread.", ref(m)); err != nil {
			log.Printf("error: reply link-cleared: %v", err)
		}
		b.maybeRefreshBriefIssues(s, threadID)
		return
	}

	// /link unlink #42  or  /unlink #42 (handled as KindLink with unlink prefix)
	unlinkArg, isUnlink := parseUnlinkArg(arg)
	if isUnlink {
		if unlinkArg == "" {
			if _, err := s.ChannelMessageSendReply(threadID, "Usage: `@Grok /unlink #42` or `@Grok /link unlink #42`", ref(m)); err != nil {
				log.Printf("error: reply unlink-usage: %v", err)
			}
			return
		}
		if !e.RemoveIssue(unlinkArg) {
			if _, err := s.ChannelMessageSendReply(threadID, fmt.Sprintf("No linked issue matching `%s`.", unlinkArg), ref(m)); err != nil {
				log.Printf("error: reply unlink-miss: %v", err)
			}
			return
		}
		if err := b.sessions.Set(threadID, e); err != nil {
			if _, sendErr := s.ChannelMessageSendReply(threadID, "Could not save: "+err.Error(), ref(m)); sendErr != nil {
				log.Printf("error: reply unlink-save: %v", sendErr)
			}
			return
		}
		if _, err := s.ChannelMessageSendReply(threadID, fmt.Sprintf("Unlinked `%s`.", unlinkArg), ref(m)); err != nil {
			log.Printf("error: reply unlink-ok: %v", err)
		}
		b.maybeRefreshBriefIssues(s, threadID)
		return
	}

	// Optional leading keyword: fix|fixes|closes|refs …
	keyword, rest := splitLinkKeyword(arg)
	refs := sessionstore.ParseIssueRefs(rest)
	if len(refs) == 0 {
		// Allow bare number without #.
		refs = sessionstore.ParseIssueRefs("#" + strings.TrimSpace(rest))
	}
	if len(refs) == 0 {
		if _, err := s.ChannelMessageSendReply(threadID,
			fmt.Sprintf("Could not parse issue from `%s`. %s", arg, linkHelpText()), ref(m)); err != nil {
			log.Printf("error: reply link-parse: %v", err)
		}
		return
	}
	owner, repo := defaultIssueRepo(e)
	sessionstore.FillIssueOwnerRepo(refs, owner, repo)
	if keyword != "" {
		for i := range refs {
			refs[i].Keyword = keyword
		}
	}

	for _, iss := range refs {
		// Explicit /link always applies the chosen keyword (including Refs after Fixes).
		e.UpsertIssueForceKeyword(iss)
	}
	if m.Author != nil {
		ensureSessionOwner(&e, m.Author.ID, m.Author.String())
	}
	if e.Project == "" {
		parentID := parentChannelID(s, threadID)
		if p, err := b.resolveProject(parentID); err == nil {
			e.Project = p.Name
		}
	}
	if err := b.sessions.Set(threadID, e); err != nil {
		if _, sendErr := s.ChannelMessageSendReply(threadID, "Could not save issues: "+err.Error(), ref(m)); sendErr != nil {
			log.Printf("error: reply link-save: %v", sendErr)
		}
		return
	}

	var parts []string
	for _, iss := range refs {
		// Re-read after upsert for merged keyword/url.
		if full, ok := e.FindIssue(iss.DisplayRef()); ok {
			iss = full
		}
		parts = append(parts, fmt.Sprintf("%s (%s)", iss.DisplayRef(), iss.EffectiveKeyword()))
	}
	msg := "Linked " + strings.Join(parts, ", ") + "."
	if _, err := s.ChannelMessageSendReply(threadID, msg, ref(m)); err != nil {
		log.Printf("error: reply link-ok: %v", err)
	}
	b.maybeRefreshBriefIssues(s, threadID)
}

func parseLinkArg(prompt string) string {
	text := strings.TrimSpace(prompt)
	if text == "" {
		return ""
	}
	lower := strings.ToLower(text)
	for _, prefix := range []string{"/link", "link", "/unlink", "unlink"} {
		if lower == prefix {
			return ""
		}
		if strings.HasPrefix(lower, prefix+" ") {
			rest := strings.TrimSpace(text[len(prefix):])
			// Preserve unlink as a subcommand token for /link unlink …
			if prefix == "/unlink" || prefix == "unlink" {
				return "unlink " + rest
			}
			return rest
		}
	}
	return strings.TrimSpace(text)
}

// parseUnlinkArg returns (query, true) for "unlink …" args.
func parseUnlinkArg(arg string) (string, bool) {
	lower := strings.ToLower(strings.TrimSpace(arg))
	if lower == "unlink" {
		return "", true
	}
	if strings.HasPrefix(lower, "unlink ") {
		return strings.TrimSpace(arg[len("unlink "):]), true
	}
	return "", false
}

// splitLinkKeyword peels optional Fixes/Refs keyword from the start of /link args.
func splitLinkKeyword(arg string) (keyword, rest string) {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return "", ""
	}
	fields := strings.Fields(arg)
	if len(fields) == 0 {
		return "", arg
	}
	kw := sessionstore.NormalizeIssueKeyword(fields[0])
	// Only treat as keyword when the token is a known alias (not a random word).
	switch strings.ToLower(fields[0]) {
	case "fix", "fixes", "close", "closes", "closed", "resolve", "resolves", "resolved",
		"ref", "refs", "reference", "references":
		if len(fields) == 1 {
			return kw, ""
		}
		return kw, strings.TrimSpace(arg[len(fields[0]):])
	default:
		return "", arg
	}
}

func formatLinkStatus(e sessionstore.Entry) string {
	if !e.HasIssues() {
		return "**issue:** (none linked)\n" + linkHelpText()
	}
	lines := sessionstore.FormatIssueStatusLines(e.Issues)
	lines = append(lines, linkHelpText())
	return strings.Join(lines, "\n")
}

func linkHelpText() string {
	return "Link: `@Grok /link #42` · `@Grok /link fix #42` · `@Grok /link https://github.com/org/repo/issues/42` · `@Grok /unlink #42` · `@Grok /link clear`"
}

func (b *Bot) maybeRefreshBriefIssues(s *discordgo.Session, threadID string) {
	if b == nil || s == nil || threadID == "" {
		return
	}
	e, ok := b.sessions.Get(threadID)
	if !ok || e.BriefMsgID == "" {
		return
	}
	if _, err := b.refreshBriefCard(s, threadID, e.Cwd); err != nil {
		log.Printf("brief: issue link refresh thread=%s: %v", threadID, err)
	}
}

// prefixThreadTitleWithIssues adds "#N " when missing.
func prefixThreadTitleWithIssues(title string, issues []sessionstore.TrackedIssue) string {
	pref := sessionstore.IssueTitlePrefix(issues)
	if pref == "" {
		return title
	}
	title = strings.TrimSpace(title)
	// Already starts with same numbers?
	trimPref := strings.TrimSpace(pref)
	if strings.HasPrefix(title, trimPref) || strings.HasPrefix(strings.ToLower(title), strings.ToLower(trimPref)) {
		return title
	}
	// Avoid double #N if title already begins with #digits.
	combined := pref + title
	if len(combined) > 100 {
		// Discord thread name limit ~100; keep prefix and trim rest.
		rest := title
		maxRest := 100 - len(pref)
		if maxRest < 10 {
			return truncateRunes(pref, 100)
		}
		if len(rest) > maxRest {
			cut := strings.LastIndex(rest[:maxRest], " ")
			if cut < maxRest/3 {
				cut = maxRest
			}
			rest = strings.TrimSpace(rest[:cut])
		}
		return pref + rest
	}
	return combined
}
