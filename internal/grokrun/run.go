package grokrun

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strings"
	"time"
)

type Options struct {
	GrokBin   string
	Prompt    string
	Cwd       string
	SessionID string
	Yolo      bool
	Model     string
	MaxTurns  int
	Timeout   time.Duration
	ExtraArgs []string
	// Tools, when non-nil, passes --tools (comma-separated allowlist).
	// Use a pointer to empty string to request no tools when the CLI supports it.
	Tools *string
	// NoSubagents / NoPlan / DisableWebSearch add corresponding headless flags.
	NoSubagents      bool
	NoPlan           bool
	NoMemory         bool
	DisableWebSearch bool

	// OnTextDelta receives each assistant text fragment when using streaming-json.
	// When set, Run uses --output-format streaming-json; otherwise json.
	OnTextDelta func(delta string)
	// OnThought receives thought/status fragments (optional; streaming only).
	OnThought func(delta string)
}

type Result struct {
	Text      string
	SessionID string
	Code      int
	Stderr    string
	// Cancelled is true when the parent context was cancelled (e.g. Discord /cancel).
	Cancelled bool
}

type jsonOut struct {
	Type      string `json:"type"`
	Text      string `json:"text"`
	Data      string `json:"data"`
	Message   string `json:"message"`
	SessionID string `json:"sessionId"`
}

// streamEvent is one NDJSON line from --output-format streaming-json.
type streamEvent struct {
	Type       string `json:"type"`
	Data       string `json:"data"`
	Text       string `json:"text"`
	Message    string `json:"message"`
	SessionID  string `json:"sessionId"`
	StopReason string `json:"stopReason"`
}

// Run executes one headless Grok Build turn.
func Run(ctx context.Context, opt Options) Result {
	if opt.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opt.Timeout)
		defer cancel()
	}

	stream := opt.OnTextDelta != nil || opt.OnThought != nil
	format := "json"
	if stream {
		format = "streaming-json"
	}

	args := []string{
		"-p", opt.Prompt,
		"--cwd", opt.Cwd,
		"--output-format", format,
		"--max-turns", fmt.Sprintf("%d", opt.MaxTurns),
		"--no-auto-update",
	}
	if opt.Yolo {
		args = append(args, "--yolo")
	}
	if opt.Model != "" {
		args = append(args, "-m", opt.Model)
	}
	if opt.SessionID != "" {
		args = append(args, "--resume", opt.SessionID)
	}
	if opt.Tools != nil {
		args = append(args, "--tools", *opt.Tools)
	}
	if opt.NoSubagents {
		args = append(args, "--no-subagents")
	}
	if opt.NoPlan {
		args = append(args, "--no-plan")
	}
	if opt.NoMemory {
		args = append(args, "--no-memory")
	}
	if opt.DisableWebSearch {
		args = append(args, "--disable-web-search")
	}
	args = append(args, opt.ExtraArgs...)

	// Log argv without dumping a huge prompt twice if already logged upstream.
	logArgs := make([]string, len(args))
	copy(logArgs, args)
	for i := 0; i+1 < len(logArgs); i++ {
		if logArgs[i] == "-p" && len(logArgs[i+1]) > 200 {
			logArgs[i+1] = logArgs[i+1][:200] + "…"
		}
	}
	log.Printf("grokrun: exec bin=%q cwd=%q format=%s args=%v", opt.GrokBin, opt.Cwd, format, logArgs)

	cmd := exec.CommandContext(ctx, opt.GrokBin, args...)
	cmd.Dir = opt.Cwd
	cmd.Env = os.Environ()

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if !stream {
		var stdout bytes.Buffer
		cmd.Stdout = &stdout
		err := cmd.Run()
		return finishResult(ctx, opt, err, stdout.Bytes(), stderr.String(), opt.Timeout)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return Result{
			Text:      fmt.Sprintf("Failed to start grok stdout pipe: %v", err),
			SessionID: opt.SessionID,
			Code:      1,
			Stderr:    stderr.String(),
		}
	}
	if err := cmd.Start(); err != nil {
		return Result{
			Text:      fmt.Sprintf("Failed to start grok: %v", err),
			SessionID: opt.SessionID,
			Code:      1,
			Stderr:    stderr.String(),
		}
	}

	text, sessionID, parseErr := consumeStream(stdout, opt.OnTextDelta, opt.OnThought)
	waitErr := cmd.Wait()

	if sessionID == "" {
		sessionID = opt.SessionID
	}

	// Prefer context cancellation over parse/wait errors.
	if res, ok := contextResult(ctx, opt, stderr.String(), opt.Timeout); ok {
		if text != "" {
			res.Text = text
		}
		if sessionID != "" {
			res.SessionID = sessionID
		}
		return res
	}

	code := 0
	if waitErr != nil {
		if ee, ok := waitErr.(*exec.ExitError); ok {
			code = ee.ExitCode()
			log.Printf("grokrun: exit code=%d err=%v stderr=%q textLen=%d",
				code, waitErr, truncate(stderr.String(), 1000), len(text))
		} else {
			log.Printf("grokrun: wait failed: %v stderr=%q", waitErr, truncate(stderr.String(), 1000))
			return Result{
				Text:      fmt.Sprintf("Failed to run grok: %v", waitErr),
				SessionID: sessionID,
				Code:      1,
				Stderr:    stderr.String(),
			}
		}
	} else {
		log.Printf("grokrun: ok stream textLen=%d stderrLen=%d", len(text), stderr.Len())
	}

	if parseErr != nil {
		log.Printf("grokrun: stream parse note: %v", parseErr)
	}

	if text == "" {
		text = strings.TrimSpace(stderr.String())
		if text == "" {
			if code != 0 {
				text = fmt.Sprintf("(grok exited %d with empty stream text)", code)
			} else {
				text = "(empty response)"
			}
		}
	}

	return Result{
		Text:      text,
		SessionID: sessionID,
		Code:      code,
		Stderr:    stderr.String(),
	}
}

func finishResult(ctx context.Context, opt Options, err error, stdout []byte, stderr string, timeout time.Duration) Result {
	if res, ok := contextResult(ctx, opt, stderr, timeout); ok && err != nil {
		return res
	}

	code := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
			log.Printf("grokrun: exit code=%d err=%v stderr=%q stdoutLen=%d",
				code, err, truncate(stderr, 1000), len(stdout))
		} else {
			log.Printf("grokrun: start failed: %v stderr=%q", err, truncate(stderr, 1000))
			return Result{
				Text:      fmt.Sprintf("Failed to start grok: %v", err),
				SessionID: opt.SessionID,
				Code:      1,
				Stderr:    stderr,
			}
		}
	} else {
		log.Printf("grokrun: ok stdoutLen=%d stderrLen=%d", len(stdout), len(stderr))
	}

	out := strings.TrimSpace(string(stdout))
	text := out
	sessionID := opt.SessionID

	var parsed jsonOut
	if err := json.Unmarshal(stdout, &parsed); err == nil {
		if parsed.Type == "error" {
			text = parsed.Message
			if text == "" {
				text = out
			}
		} else if parsed.Text != "" {
			text = parsed.Text
		}
		if parsed.SessionID != "" {
			sessionID = parsed.SessionID
		}
	} else if out == "" {
		text = strings.TrimSpace(stderr)
		if text == "" {
			text = fmt.Sprintf("(grok exited %d with empty stdout)", code)
		}
	}

	if text == "" {
		text = "(empty response)"
	}

	return Result{
		Text:      text,
		SessionID: sessionID,
		Code:      code,
		Stderr:    stderr,
	}
}

func contextResult(ctx context.Context, opt Options, stderr string, timeout time.Duration) (Result, bool) {
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		log.Printf("grokrun: timeout after %s stderr=%q", timeout, truncate(stderr, 1000))
		return Result{
			Text:      fmt.Sprintf("Timed out after %s. Partial work may exist in the Grok session.", timeout),
			SessionID: opt.SessionID,
			Code:      124,
			Stderr:    stderr,
		}, true
	case ctx.Err() != nil:
		log.Printf("grokrun: cancelled stderr=%q", truncate(stderr, 1000))
		return Result{
			Text:      "Cancelled. Partial work may exist in the Grok session.",
			SessionID: opt.SessionID,
			Code:      130,
			Stderr:    stderr,
			Cancelled: true,
		}, true
	default:
		return Result{}, false
	}
}

// consumeStream reads NDJSON streaming-json events until EOF.
func consumeStream(r io.Reader, onText, onThought func(string)) (text, sessionID string, err error) {
	sc := bufio.NewScanner(r)
	// Thoughts + text can be large; allow big lines.
	sc.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)

	var b strings.Builder
	var parseNotes []string

	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var ev streamEvent
		if jerr := json.Unmarshal([]byte(line), &ev); jerr != nil {
			parseNotes = append(parseNotes, jerr.Error())
			continue
		}
		switch strings.ToLower(ev.Type) {
		case "text":
			delta := ev.Data
			if delta == "" {
				delta = ev.Text
			}
			if delta != "" {
				b.WriteString(delta)
				if onText != nil {
					onText(delta)
				}
			}
		case "thought":
			delta := ev.Data
			if delta == "" {
				delta = ev.Text
			}
			if delta != "" && onThought != nil {
				onThought(delta)
			}
		case "end":
			if ev.SessionID != "" {
				sessionID = ev.SessionID
			}
		case "error":
			msg := ev.Message
			if msg == "" {
				msg = ev.Data
			}
			if msg == "" {
				msg = ev.Text
			}
			if msg != "" {
				if b.Len() > 0 {
					b.WriteString("\n\n")
				}
				b.WriteString(msg)
				if onText != nil {
					onText(msg)
				}
			}
		default:
			// Ignore tool/other events; session id may appear on them too.
			if ev.SessionID != "" {
				sessionID = ev.SessionID
			}
		}
	}
	if scanErr := sc.Err(); scanErr != nil {
		err = scanErr
	} else if len(parseNotes) > 0 {
		err = fmt.Errorf("skipped %d malformed lines (e.g. %s)", len(parseNotes), parseNotes[0])
	}
	return b.String(), sessionID, err
}

// SummarizeTitle asks Grok for a short Discord thread title (separate one-shot
// session, no resume into the work session). On failure, ok is false.
func SummarizeTitle(ctx context.Context, grokBin, model, taskPrompt, cwd string, timeout time.Duration) (title string, ok bool) {
	if strings.TrimSpace(taskPrompt) == "" {
		return "", false
	}
	if cwd == "" {
		cwd = os.TempDir()
	}
	if timeout <= 0 {
		timeout = 45 * time.Second
	}

	noTools := ""
	prompt := strings.Join([]string{
		"You name Discord threads for an engineering team.",
		"Given the user task below, reply with ONLY a short thread title.",
		"Rules:",
		"- 3 to 10 words",
		"- under 80 characters",
		"- no quotes, no markdown, no trailing punctuation",
		"- no leading labels like Title:",
		"- describe the task, not the user",
		"",
		"Task:",
		taskPrompt,
	}, "\n")

	result := Run(ctx, Options{
		GrokBin:          grokBin,
		Prompt:           prompt,
		Cwd:              cwd,
		Yolo:             false,
		Model:            model,
		MaxTurns:         1,
		Timeout:          timeout,
		Tools:            &noTools,
		NoSubagents:      true,
		NoPlan:           true,
		NoMemory:         true,
		DisableWebSearch: true,
		ExtraArgs:        []string{"--verbatim"},
	})
	if result.Code != 0 {
		log.Printf("grokrun: summarize failed code=%d text=%q stderr=%q",
			result.Code, truncate(result.Text, 200), truncate(result.Stderr, 400))
		return "", false
	}

	title = cleanTitle(result.Text)
	if title == "" {
		return "", false
	}
	log.Printf("grokrun: summarize title=%q", title)
	return title, true
}

func cleanTitle(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	// First non-empty line only.
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		s = line
		break
	}
	s = strings.Trim(s, "\"'`*")
	s = strings.TrimPrefix(s, "Title:")
	s = strings.TrimPrefix(s, "title:")
	s = strings.TrimSpace(s)
	s = strings.Join(strings.Fields(s), " ")
	if s == "" || s == "(empty response)" {
		return ""
	}
	return s
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
