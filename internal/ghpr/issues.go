package ghpr

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// DefaultIssueBodyCap is the max body/comment size kept for prompts/UI.
const DefaultIssueBodyCap = 32 * 1024

// IssueListOpts controls gh issue list.
type IssueListOpts struct {
	// Owner/Repo force --repo owner/repo (recommended for multi-repo projects).
	Owner string
	Repo  string
	// State: open (default), closed, all.
	State string
	// Limit defaults to 30.
	Limit int
	// Labels optional filter.
	Labels []string
}

// IssueComment is one issue comment.
type IssueComment struct {
	Author string
	Body   string
	URL    string
}

// IssueInfo is a GitHub issue snapshot for web/read surfaces.
type IssueInfo struct {
	Number    int
	URL       string
	Title     string
	State     string // OPEN, CLOSED
	Body      string
	Author    string
	Labels    []string
	Comments  []IssueComment
	Owner     string
	Repo      string
	Truncated bool // body or comments hit size caps
}

// ListIssues lists issues for a repo via gh.
func ListIssues(ctx context.Context, repoDir string, opts IssueListOpts) ([]IssueInfo, error) {
	return ListIssuesWith(ctx, defaultRunner, repoDir, opts)
}

// ListIssuesWith is ListIssues with an injectable runner.
func ListIssuesWith(ctx context.Context, run Runner, repoDir string, opts IssueListOpts) ([]IssueInfo, error) {
	if run == nil {
		run = defaultRunner
	}
	state := strings.ToLower(strings.TrimSpace(opts.State))
	if state == "" {
		state = "open"
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = 30
	}
	args := []string{"issue", "list",
		"--state", state,
		"--limit", strconv.Itoa(limit),
		"--json", "number,url,title,state,author,labels,body",
	}
	if o, r := strings.TrimSpace(opts.Owner), strings.TrimSpace(opts.Repo); o != "" && r != "" {
		args = append(args, "--repo", o+"/"+r)
	}
	for _, lab := range opts.Labels {
		lab = strings.TrimSpace(lab)
		if lab != "" {
			args = append(args, "--label", lab)
		}
	}
	raw, err := run(ctx, repoDir, "gh", args...)
	if err != nil {
		return nil, err
	}
	return parseIssueListJSON(raw, opts.Owner, opts.Repo)
}

// ViewIssue loads one issue including comments (body capped).
func ViewIssue(ctx context.Context, repoDir string, number int) (IssueInfo, error) {
	return ViewIssueWith(ctx, defaultRunner, repoDir, number, "", "")
}

// ViewIssueWith loads an issue; owner/repo optional --repo override.
func ViewIssueWith(ctx context.Context, run Runner, repoDir string, number int, owner, repo string) (IssueInfo, error) {
	if run == nil {
		run = defaultRunner
	}
	if number <= 0 {
		return IssueInfo{}, fmt.Errorf("invalid issue number")
	}
	args := []string{"issue", "view", strconv.Itoa(number),
		"--json", "number,url,title,state,author,labels,body,comments",
	}
	if o, r := strings.TrimSpace(owner), strings.TrimSpace(repo); o != "" && r != "" {
		args = append(args, "--repo", o+"/"+r)
	}
	raw, err := run(ctx, repoDir, "gh", args...)
	if err != nil {
		return IssueInfo{}, err
	}
	return parseIssueViewJSON(raw, owner, repo, DefaultIssueBodyCap)
}

type issueJSON struct {
	Number  int    `json:"number"`
	URL     string `json:"url"`
	Title   string `json:"title"`
	State   string `json:"state"`
	Body    string `json:"body"`
	Author  any    `json:"author"` // {login} or string
	Labels  []any  `json:"labels"`
	Comments []struct {
		Author any    `json:"author"`
		Body   string `json:"body"`
		URL    string `json:"url"`
	} `json:"comments"`
}

func parseIssueListJSON(raw []byte, owner, repo string) ([]IssueInfo, error) {
	var rows []issueJSON
	if err := json.Unmarshal(raw, &rows); err != nil {
		return nil, fmt.Errorf("gh issue list json: %w", err)
	}
	out := make([]IssueInfo, 0, len(rows))
	for _, r := range rows {
		info, _ := r.toInfo(owner, repo, DefaultIssueBodyCap)
		out = append(out, info)
	}
	return out, nil
}

func parseIssueViewJSON(raw []byte, owner, repo string, bodyCap int) (IssueInfo, error) {
	var row issueJSON
	if err := json.Unmarshal(raw, &row); err != nil {
		return IssueInfo{}, fmt.Errorf("gh issue view json: %w", err)
	}
	return row.toInfo(owner, repo, bodyCap)
}

func (r issueJSON) toInfo(owner, repo string, bodyCap int) (IssueInfo, error) {
	info := IssueInfo{
		Number: r.Number,
		URL:    r.URL,
		Title:  r.Title,
		State:  strings.ToUpper(strings.TrimSpace(r.State)),
		Author: authorLogin(r.Author),
		Owner:  strings.TrimSpace(owner),
		Repo:   strings.TrimSpace(repo),
	}
	body, trunc := truncateBytes(r.Body, bodyCap)
	info.Body = body
	info.Truncated = trunc
	for _, lab := range r.Labels {
		info.Labels = append(info.Labels, labelName(lab))
	}
	for _, c := range r.Comments {
		cb, ct := truncateBytes(c.Body, bodyCap)
		if ct {
			info.Truncated = true
		}
		info.Comments = append(info.Comments, IssueComment{
			Author: authorLogin(c.Author),
			Body:   cb,
			URL:    c.URL,
		})
	}
	if info.Owner == "" || info.Repo == "" {
		fillIssueOwnerRepo(&info)
	}
	return info, nil
}

func fillIssueOwnerRepo(info *IssueInfo) {
	// https://github.com/owner/repo/issues/N
	const prefix = "https://github.com/"
	u := strings.TrimSpace(info.URL)
	if !strings.HasPrefix(strings.ToLower(u), prefix) {
		return
	}
	rest := u[len(prefix):]
	parts := strings.Split(rest, "/")
	if len(parts) < 2 {
		return
	}
	if info.Owner == "" {
		info.Owner = parts[0]
	}
	if info.Repo == "" {
		info.Repo = parts[1]
	}
}

func authorLogin(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case map[string]any:
		if s, ok := t["login"].(string); ok {
			return s
		}
	}
	return ""
}

func labelName(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case map[string]any:
		if s, ok := t["name"].(string); ok {
			return s
		}
	}
	return fmt.Sprint(v)
}

func truncateBytes(s string, max int) (string, bool) {
	if max <= 0 || len(s) <= max {
		return s, false
	}
	// Prefer rune-safe cut near max.
	r := []rune(s)
	if len(r) <= max {
		return s, false
	}
	// max is bytes; approximate with runes under max bytes.
	cut := max
	if cut > len(r) {
		cut = len(r)
	}
	// Walk until byte length near max.
	out := string(r)
	if len(out) <= max {
		return out, false
	}
	for cut > 0 && len(string(r[:cut])) > max-1 {
		cut--
	}
	if cut < 1 {
		cut = 1
	}
	return string(r[:cut]) + "…", true
}
