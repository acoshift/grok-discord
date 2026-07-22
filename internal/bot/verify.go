package bot

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"os/exec"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/acoshift/grokwork/internal/config"
	"github.com/acoshift/grokwork/internal/sessionstore"
)

const (
	defaultVerifyTimeoutMs = 600_000
	maxVerifyLogBytes      = 8 * 1024
)

// handleVerify: @Grok /verify [name]
// Runs project verifyCommands without Grok; posts pass/fail card.
func (b *Bot) handleVerify(s *discordgo.Session, m *discordgo.MessageCreate, parsed Parsed) {
	if !isThread(s, m.ChannelID) {
		replyText(s, m, "Use `@Grok /verify` inside a Grok thread.")
		return
	}
	e, ok := b.sessions.Get(m.ChannelID)
	if !ok {
		replyText(s, m, "No session for this thread yet.")
		return
	}
	project := e.Project
	if project == "" {
		parentID := parentChannelID(s, m.ChannelID)
		if p, err := b.resolveProject(parentID); err == nil {
			project = p.Name
		}
	}
	cmds := b.cfg.ProjectVerifyCommands(project)
	if len(cmds) == 0 {
		replyText(s, m, "No verify commands configured for this project. Admin: set `projects.*.verifyCommands` in config (name + command).")
		return
	}
	want := strings.TrimSpace(stripCmdPrefix(parsed.Prompt, "/verify", "verify"))
	toRun := filterVerifyCmds(cmds, want)
	if len(toRun) == 0 {
		var names []string
		for _, c := range cmds {
			names = append(names, c.Name)
		}
		replyText(s, m, fmt.Sprintf("Unknown verify `%s`. Configured: `%s`", want, strings.Join(names, "`, `")))
		return
	}
	cwd := e.Cwd
	if cwd == "" {
		if p, ok := b.cfg.ProjectPath(project); ok {
			cwd = p
		}
	}
	if cwd == "" {
		replyText(s, m, "No cwd for verify.")
		return
	}

	replyText(s, m, fmt.Sprintf("Running **%d** verify command(s)…", len(toRun)))
	var cards []string
	allOK := true
	for _, cmd := range toRun {
		res := b.runOneVerify(cwd, cmd)
		cards = append(cards, formatVerifyCard(res))
		if !res.OK {
			allOK = false
		}
		log.Printf("verify.run thread=%s name=%s ok=%v exit=%d", m.ChannelID, res.Name, res.OK, res.ExitCode)
	}
	body := strings.Join(cards, "\n\n")
	if !allOK {
		body = "**Verify failed**\n\n" + body
	} else {
		body = "**Verify passed**\n\n" + body
	}
	msg, err := s.ChannelMessageSend(m.ChannelID, sanitizeDiscordContent(clampDiscord(body)))
	if err != nil {
		log.Printf("error: verify card: %v", err)
		return
	}
	_, _, _ = b.sessions.Patch(m.ChannelID, func(ent *sessionstore.Entry) {
		ent.VerifyMsgID = msg.ID
	})
}

type verifyResult struct {
	Name     string
	Command  string
	OK       bool
	ExitCode int
	Log      string
	Elapsed  time.Duration
	Err      string
}

func (b *Bot) runOneVerify(cwd string, cmd config.VerifyCommand) verifyResult {
	timeoutMs := cmd.TimeoutMs
	if timeoutMs <= 0 {
		timeoutMs = defaultVerifyTimeoutMs
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutMs)*time.Millisecond)
	defer cancel()
	start := time.Now()
	// shell -c for project-defined commands (trusted admin config).
	c := exec.CommandContext(ctx, "sh", "-c", cmd.Command)
	c.Dir = cwd
	var buf bytes.Buffer
	c.Stdout = &buf
	c.Stderr = &buf
	err := c.Run()
	elapsed := time.Since(start)
	res := verifyResult{
		Name:    cmd.Name,
		Command: cmd.Command,
		Elapsed: elapsed,
		Log:     truncateBytes(buf.String(), maxVerifyLogBytes),
	}
	if ctx.Err() == context.DeadlineExceeded {
		res.OK = false
		res.ExitCode = -1
		res.Err = "timeout"
		return res
	}
	if err != nil {
		res.OK = false
		if ee, ok := err.(*exec.ExitError); ok {
			res.ExitCode = ee.ExitCode()
		} else {
			res.ExitCode = -1
			res.Err = err.Error()
		}
		return res
	}
	res.OK = true
	res.ExitCode = 0
	return res
}

func filterVerifyCmds(cmds []config.VerifyCommand, want string) []config.VerifyCommand {
	want = strings.TrimSpace(strings.ToLower(want))
	if want == "" || want == "all" {
		return cmds
	}
	var out []config.VerifyCommand
	for _, c := range cmds {
		if strings.EqualFold(c.Name, want) {
			out = append(out, c)
		}
	}
	return out
}

func formatVerifyCard(r verifyResult) string {
	status := "pass"
	if !r.OK {
		status = "fail"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "**`%s`** · **%s** · exit `%d` · %s\n", r.Name, status, r.ExitCode, r.Elapsed.Round(time.Millisecond))
	fmt.Fprintf(&b, "```\n%s\n```\n", r.Command)
	if r.Err != "" {
		fmt.Fprintf(&b, "_%s_\n", r.Err)
	}
	if log := strings.TrimSpace(r.Log); log != "" {
		if len(log) > 1500 {
			log = log[len(log)-1500:]
			log = "…\n" + log
		}
		fmt.Fprintf(&b, "```\n%s\n```", log)
	}
	return b.String()
}

func truncateBytes(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	return s[len(s)-max:]
}

func clampDiscord(s string) string {
	if len(s) <= maxMsg {
		return s
	}
	return s[:maxMsg-20] + "\n…(truncated)"
}
