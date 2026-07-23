package web

import (
	"fmt"
	"strings"

	"github.com/moonrhythm/hime"

	"github.com/acoshift/grokwork/internal/config"
)

// ── Per-project settings tabs (/config/projects/{name}[/tab]) ────────────
//
// Project settings split into four sub-tab pages, each its own URL under the
// boosted shell: Access (team policy + unified member roster), Workflow
// (shipping + session defaults), Integrations (Discord / GitHub / Linear /
// repo fetch), and Danger (remove project). POSTs land back on their tab via
// projectConfigTabRedirect.

// memberRow is one principal (user or role) on the Access roster: allowlist
// membership plus optional explicit capability template, with the effective
// capabilities ResolveCapabilities would grant on Discord.
type memberRow struct {
	ID       string
	Name     string // display name (users only; best-effort)
	Initials string // avatar fallback ("@" for roles)
	IsRole   bool
	Template string   // explicit template; "" = default fallback
	Explicit bool     // Template != ""
	Caps     []string // effective capability chips
	// TemplateUnknown: explicit template that resolves to nothing (typo in a
	// hand-edited config). The select shows it as "(unknown)" instead of
	// silently falling back to default; effective caps come from the fallback.
	TemplateUnknown bool
	// Inert: capability map entry without allowlist membership (legacy or
	// hand-edited config) — the bot never grants these access, so the roster
	// surfaces them for cleanup instead of hiding them.
	Inert bool
}

// capMatrixRow is one template line in the "what each role can do" legend.
type capMatrixRow struct {
	Name  string
	Flags []bool // aligned with capColumns labels
}

// capColumns orders capability flags for chips and the legend matrix.
// RequestChange/SafeOps are wave-1 reserved (no command gates) — omitted.
var capColumns = []struct {
	Label string
	Get   func(config.Capabilities) bool
}{
	{"investigate", func(c config.Capabilities) bool { return c.Investigate }},
	{"draft reply", func(c config.Capabilities) bool { return c.DraftCustomerReply }},
	{"escalate", func(c config.Capabilities) bool { return c.FileEscalation }},
	{"sessions", func(c config.Capabilities) bool { return c.StartSessions }},
	{"github", func(c config.Capabilities) bool { return c.GithubWrites }},
	{"merge", func(c config.Capabilities) bool { return c.Merge }},
	{"approve", func(c config.Capabilities) bool { return c.Approve }},
	{"admin", func(c config.Capabilities) bool { return c.AdminProject }},
}

func capChips(c config.Capabilities) []string {
	var out []string
	for _, col := range capColumns {
		if col.Get(c) {
			out = append(out, col.Label)
		}
	}
	return out
}

func capFlags(c config.Capabilities) []bool {
	out := make([]bool, len(capColumns))
	for i, col := range capColumns {
		out[i] = col.Get(c)
	}
	return out
}

func memberInitials(name, id string) string {
	s := strings.TrimSpace(name)
	if s == "" {
		s = strings.TrimSpace(id)
	}
	r := []rune(s)
	if len(r) > 2 {
		r = r[:2]
	}
	return strings.ToLower(string(r))
}

// buildMemberRoster merges the allowlist and capability maps into one roster:
// members first (users then roles, config order), then inert map-only rows.
func (s *Server) buildMemberRoster(item *config.ProjectItem, names map[string]string) []memberRow {
	tplByUser := make(map[string]string, len(item.CapabilityByUser))
	for _, m := range item.CapabilityByUser {
		tplByUser[m.ID] = m.Template
	}
	tplByRole := make(map[string]string, len(item.CapabilityByRole))
	for _, m := range item.CapabilityByRole {
		tplByRole[m.ID] = m.Template
	}
	known := make(map[string]bool, len(item.CapabilityTemplateNames))
	for _, n := range item.CapabilityTemplateNames {
		known[n] = true
	}
	var rows []memberRow
	member := make(map[string]bool, len(item.AllowedUserIDs)+len(item.AllowedRoleIDs))
	for _, id := range item.AllowedUserIDs {
		member["u:"+id] = true
		tpl := tplByUser[id]
		rows = append(rows, memberRow{
			ID:              id,
			Name:            names[id],
			Initials:        memberInitials(names[id], id),
			Template:        tpl,
			Explicit:        tpl != "",
			TemplateUnknown: tpl != "" && !known[tpl],
			Caps:            capChips(s.cfg.ResolveCapabilities(item.Name, id, nil)),
		})
	}
	for _, id := range item.AllowedRoleIDs {
		member["r:"+id] = true
		tpl := tplByRole[id]
		rows = append(rows, memberRow{
			ID:              id,
			Initials:        "@",
			IsRole:          true,
			Template:        tpl,
			Explicit:        tpl != "",
			TemplateUnknown: tpl != "" && !known[tpl],
			Caps:            capChips(s.cfg.ResolveCapabilities(item.Name, "", []string{id})),
		})
	}
	for _, m := range item.CapabilityByUser {
		if !member["u:"+m.ID] {
			rows = append(rows, memberRow{
				ID: m.ID, Name: names[m.ID], Initials: "?",
				Template: m.Template, Explicit: true, Inert: true,
			})
		}
	}
	for _, m := range item.CapabilityByRole {
		if !member["r:"+m.ID] {
			rows = append(rows, memberRow{
				ID: m.ID, Initials: "@", IsRole: true,
				Template: m.Template, Explicit: true, Inert: true,
			})
		}
	}
	return rows
}

// buildCapMatrix renders each known template (builtin + project overlays)
// against the capability columns for the Access legend.
func (s *Server) buildCapMatrix(item *config.ProjectItem) ([]capMatrixRow, []string) {
	names := make([]string, len(capColumns))
	for i, col := range capColumns {
		names[i] = col.Label
	}
	rows := make([]capMatrixRow, 0, len(item.CapabilityTemplateNames))
	for _, tpl := range item.CapabilityTemplateNames {
		caps, ok := s.cfg.ResolveTemplate(item.Name, tpl)
		if !ok {
			continue
		}
		rows = append(rows, capMatrixRow{Name: tpl, Flags: capFlags(caps)})
	}
	return rows, names
}

// projectConfigTab locates the project, fills the shared settings chrome for
// one tab, then renders. Unknown project → config hub with err.
func (s *Server) projectConfigTab(ctx *hime.Context, tab, tmpl string, fill func(d *pageData)) error {
	name := ctx.PathValue("name")
	snap := s.cfg.Snapshot()
	var item *config.ProjectItem
	for i := range snap.Projects {
		if snap.Projects[i].Name == name {
			item = &snap.Projects[i]
			break
		}
	}
	if item == nil {
		return ctx.RedirectTo("config", map[string]string{"err": fmt.Sprintf("unknown project %q", name)})
	}
	d := s.basePage(ctx)
	d.Title = item.Name + " · Config"
	d.IsConfig = true
	d.Config = snap
	d.Project = item.Name
	d.ProjectItem = *item
	d.ProjectTab = tab
	d.Flash = ctx.FormValue("ok")
	d.Error = ctx.FormValue("err")
	if fill != nil {
		fill(&d)
	}
	return s.viewPage(ctx, tmpl, d)
}

// projectConfigPage is the Access tab (default): team policy + member roster.
func (s *Server) projectConfigPage(ctx *hime.Context) error {
	return s.projectConfigTab(ctx, "access", "project_config", func(d *pageData) {
		item := &d.ProjectItem
		nameIDs := append([]string{}, item.AllowedUserIDs...)
		for _, m := range item.CapabilityByUser {
			nameIDs = append(nameIDs, m.ID)
		}
		names := s.resolveDiscordUserNames(nameIDs)
		d.DiscordUserNames = names
		d.Members = s.buildMemberRoster(item, names)
		d.CapMatrix, d.CapNames = s.buildCapMatrix(item)
		// Effective role for members without an explicit one: safe team off
		// falls back to builder (backward compat), on → the default template.
		if item.SafeTeamMode {
			d.DefaultRoleFallback = item.SafeTeamDefaultTemplate
		} else {
			d.DefaultRoleFallback = "builder"
		}
	})
}

func (s *Server) projectConfigWorkflowPage(ctx *hime.Context) error {
	return s.projectConfigTab(ctx, "workflow", "project_config_workflow", nil)
}

func (s *Server) projectConfigIntegrationsPage(ctx *hime.Context) error {
	return s.projectConfigTab(ctx, "integrations", "project_config_integrations", nil)
}

func (s *Server) projectConfigDangerPage(ctx *hime.Context) error {
	return s.projectConfigTab(ctx, "danger", "project_config_danger", nil)
}
