package ghpr

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// PRDetail is a richer PR snapshot for the web ship surface (beyond status cards).
type PRDetail struct {
	Info
	Body       string
	Mergeable  string // MERGEABLE, CONFLICTING, UNKNOWN, …
	BaseRef    string
	Author     string
	Additions  int
	Deletions  int
	ChangedFiles int
	Truncated  bool
}

// ViewPRDetail loads PR fields including body and merge metadata.
func ViewPRDetail(ctx context.Context, repoDir, selector string) (PRDetail, error) {
	return ViewPRDetailWith(ctx, defaultRunner, repoDir, selector)
}

// ViewPRDetailWith is ViewPRDetail with an injectable runner.
func ViewPRDetailWith(ctx context.Context, run Runner, repoDir, selector string) (PRDetail, error) {
	if run == nil {
		run = defaultRunner
	}
	selector = strings.TrimSpace(selector)
	if selector == "" {
		return PRDetail{}, fmt.Errorf("empty PR selector")
	}
	raw, err := run(ctx, repoDir, "gh", "pr", "view", selector,
		"--json", "number,url,title,state,isDraft,reviewDecision,headRefOid,headRefName,baseRefName,body,mergeable,author,additions,deletions,changedFiles")
	if err != nil {
		return PRDetail{}, err
	}
	d, err := parsePRDetailJSON(raw)
	if err != nil {
		return PRDetail{}, err
	}
	fillOwnerRepo(&d.Info)
	sel := d.URL
	if sel == "" {
		sel = selector
	}
	if sum, cErr := ChecksSummaryWith(ctx, run, repoDir, sel); cErr == nil {
		d.Checks = sum
	}
	return d, nil
}

type prDetailJSON struct {
	Number         int    `json:"number"`
	URL            string `json:"url"`
	Title          string `json:"title"`
	State          string `json:"state"`
	IsDraft        bool   `json:"isDraft"`
	ReviewDecision string `json:"reviewDecision"`
	HeadRefOid     string `json:"headRefOid"`
	HeadRefName    string `json:"headRefName"`
	BaseRefName    string `json:"baseRefName"`
	Body           string `json:"body"`
	Mergeable      string `json:"mergeable"`
	Author         any    `json:"author"`
	Additions      int    `json:"additions"`
	Deletions      int    `json:"deletions"`
	ChangedFiles   int    `json:"changedFiles"`
}

func parsePRDetailJSON(raw []byte) (PRDetail, error) {
	var j prDetailJSON
	if err := json.Unmarshal(raw, &j); err != nil {
		return PRDetail{}, fmt.Errorf("gh pr view detail json: %w", err)
	}
	body, trunc := truncateBytes(j.Body, DefaultIssueBodyCap)
	d := PRDetail{
		Info: Info{
			Number:         j.Number,
			URL:            j.URL,
			Title:          j.Title,
			State:          j.State,
			IsDraft:        j.IsDraft,
			ReviewDecision: j.ReviewDecision,
			HeadSHA:        j.HeadRefOid,
			HeadRef:        j.HeadRefName,
		},
		Body:         body,
		Mergeable:    j.Mergeable,
		BaseRef:      j.BaseRefName,
		Author:       authorLogin(j.Author),
		Additions:    j.Additions,
		Deletions:    j.Deletions,
		ChangedFiles: j.ChangedFiles,
		Truncated:    trunc,
	}
	return d, nil
}
