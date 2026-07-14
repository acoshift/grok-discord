package bot

import (
	"fmt"
	"regexp"
	"strings"
)

type Kind int

const (
	KindEmpty Kind = iota
	KindHelp
	KindProjects
	KindReset
	KindStatus
	KindCancel
	KindTask
)

type Parsed struct {
	Kind   Kind
	Prompt string
}

var mentionRE = regexp.MustCompile(`<@!?\d+>`)

func ParseMessage(content, botUserID string) Parsed {
	text := content
	if botUserID != "" {
		re := regexp.MustCompile(fmt.Sprintf(`<@!?%s>`, regexp.QuoteMeta(botUserID)))
		text = re.ReplaceAllString(text, " ")
	} else {
		text = mentionRE.ReplaceAllString(text, " ")
	}
	text = strings.Join(strings.Fields(text), " ")
	text = strings.TrimSpace(text)

	if text == "" {
		return Parsed{Kind: KindEmpty}
	}

	switch strings.ToLower(text) {
	case "/help", "help":
		return Parsed{Kind: KindHelp}
	case "/projects", "projects":
		return Parsed{Kind: KindProjects}
	case "/reset", "reset":
		return Parsed{Kind: KindReset}
	case "/status", "status":
		return Parsed{Kind: KindStatus}
	case "/cancel", "cancel", "/stop", "stop":
		return Parsed{Kind: KindCancel}
	}

	return Parsed{Kind: KindTask, Prompt: text}
}

func HelpText() string {
	return strings.Join([]string{
		"**Grok Discord bridge** — runs Grok Build on this machine against local code.",
		"",
		"**Usage**",
		"• `@Grok <task>` — run against this channel's configured project",
		"• `@Grok <follow-up>` in the same thread — resume session",
		"• Follow-ups while a run is active are queued (up to 5) and run in order",
		"• Attach logs/screenshots/patches with your message — files are downloaded for Grok to read",
		"• Or post a file, then **reply** with `@Grok <task>` — Grok reads the referenced message too",
		"",
		"Project is fixed per Discord channel (admin `channels` config). Users cannot switch projects.",
		"Each thread uses an isolated git worktree (when the project is a git repo). `/reset` removes it.",
		"Code changes are pushed and opened as a pull request (not left as local-only commits).",
		"",
		"**Commands** (mention the bot first)",
		"• `/projects` — show this channel's project",
		"• `/reset` — forget this thread's session and remove its worktree",
		"• `/status` — show this thread's session (and queue depth if busy)",
		"• `/cancel` — stop the current run (queued follow-ups still run)",
		"• `/help` — this message",
	}, "\n")
}
