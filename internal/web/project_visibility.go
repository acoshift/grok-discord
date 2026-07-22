package web

import (
	"net/http"

	"github.com/moonrhythm/hime"

	"github.com/acoshift/grokwork/internal/bot"
	"github.com/acoshift/grokwork/internal/config"
)

// visibleProjectSet is the session's allowed project names (nil = unrestricted admin).
func (s *Server) visibleProjectSet(ctx *hime.Context) map[string]struct{} {
	userID, role := s.sessionIdentity(ctx)
	return s.visibleProjectSetFor(userID, role)
}

func (s *Server) visibleProjectSetFor(userID string, role config.WebRole) map[string]struct{} {
	if config.RoleAtLeast(role, config.WebRoleAdmin) {
		return nil
	}
	if s.cfg == nil {
		return map[string]struct{}{}
	}
	names := s.cfg.ProjectsVisibleTo(userID, role)
	set := make(map[string]struct{}, len(names))
	for _, n := range names {
		set[n] = struct{}{}
	}
	return set
}

// statusVisible returns StatusSnapshot limited to projects the session may see.
// Admins (and auth-off) get the full snapshot.
func (s *Server) statusVisible(ctx *hime.Context) bot.StatusSnapshot {
	userID, role := s.sessionIdentity(ctx)
	return s.filterStatusVisible(s.bot.StatusSnapshot(), userID, role)
}

// statusVisibleHTTP is statusVisible for handlers with *http.Request (SSE).
func (s *Server) statusVisibleHTTP(r *http.Request) bot.StatusSnapshot {
	userID, role := s.sessionIdentityHTTP(r)
	return s.filterStatusVisible(s.bot.StatusSnapshot(), userID, role)
}

func (s *Server) sessionIdentityHTTP(r *http.Request) (userID string, role config.WebRole) {
	if s == nil || s.cfg == nil || !s.cfg.WebAuthEnabled() {
		return "", config.WebRoleAdmin
	}
	sess := sessionFromContext(r.Context())
	if sess == nil {
		sess = s.sessionFromRequest(r)
	}
	if sess == nil {
		return "", config.WebRoleNone
	}
	return sess.DiscordUserID, sess.Role
}

func (s *Server) filterStatusVisible(snap bot.StatusSnapshot, userID string, role config.WebRole) bot.StatusSnapshot {
	allowed := s.visibleProjectSetFor(userID, role)
	if allowed == nil {
		return snap
	}
	runs := make([]bot.ActiveRun, 0, len(snap.ActiveRuns))
	queued := 0
	for _, r := range snap.ActiveRuns {
		if _, ok := allowed[r.Project]; ok {
			runs = append(runs, r)
			queued += r.QueueLen
		}
	}
	snap.ActiveRuns = runs
	snap.ActiveCount = len(runs)
	// Only queues on visible active runs are counted; idle queues on hidden
	// projects stay out of the dashboard (those threads are not listed).
	snap.QueuedTotal = queued
	if s.cfg != nil {
		snap.ProjectCount = len(s.cfg.ProjectsVisibleTo(userID, role))
	} else {
		snap.ProjectCount = 0
	}
	return snap
}

// listShipBoardVisible is ListShipBoard scoped to projects the session may see.
func (s *Server) listShipBoardVisible(ctx *hime.Context, projectFilter, stateFilter string) bot.ShipBoard {
	_, role := s.sessionIdentity(ctx)
	if config.RoleAtLeast(role, config.WebRoleAdmin) {
		return s.bot.ListShipBoard(projectFilter, stateFilter)
	}
	return s.bot.ListShipBoardAmong(projectFilter, stateFilter, s.filterProjectNames(ctx))
}
