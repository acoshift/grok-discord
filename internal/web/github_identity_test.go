package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/acoshift/grokwork/internal/audit"
	"github.com/acoshift/grokwork/internal/config"
)

func TestConfigGitHubMapSectionRenders(t *testing.T) {
	srv, cfg, _ := authOnServer(t)
	if err := cfg.SetGitHubIdentity("99", config.GitHubIdentity{Login: "alice", Name: "Alice"}); err != nil {
		t.Fatal(err)
	}
	sid, _, err := srv.LoginAs("admin-1", "Admin", config.WebRoleAdmin)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/config", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: sid})
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d", w.Code)
	}
	body := w.Body.String()
	for _, want := range []string{
		`id="page-config"`,
		`id="github-attribution"`,
		`id="github-identity-form"`,
		`id="github-identity-table"`,
		`href="#github-attribution"`,
		"Discord user → GitHub login",
		"@alice",
		"99",
		`action="/config/github-identities"`,
		`action="/config/github-identities/remove"`,
		`name="discordUserId"`,
		`name="login"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %q in config HTML", want)
		}
	}
	if strings.Contains(body, `id="github-identity-empty"`) {
		t.Fatal("empty state should not show when map has rows")
	}
}

func TestConfigGitHubMapEmptyState(t *testing.T) {
	srv, _, _ := authOnServer(t)
	sid, _, err := srv.LoginAs("admin-1", "Admin", config.WebRoleAdmin)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/config", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: sid})
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `id="github-identity-empty"`) {
		t.Fatal("want empty state")
	}
	if !strings.Contains(body, "No Discord users mapped yet") {
		t.Fatal("want empty copy")
	}
	if strings.Contains(body, `id="github-identity-table"`) {
		t.Fatal("table should be absent when empty")
	}
}

func TestSetAndRemoveGitHubIdentityViaConfigUI(t *testing.T) {
	srv, cfg, _ := authOnServer(t)
	sid, csrf, err := srv.LoginAs("admin-1", "Admin", config.WebRoleAdmin)
	if err != nil {
		t.Fatal(err)
	}
	// Set
	form := url.Values{
		"discordUserId": {"4242"},
		"login":         {"@bob"},
		"name":          {"Bob"},
		"email":         {"bob@example.com"},
		"csrf":          {csrf},
	}
	req := httptest.NewRequest(http.MethodPost, "/config/github-identities", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: sid})
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusFound && w.Code != http.StatusSeeOther {
		t.Fatalf("set status=%d body=%s", w.Code, w.Body.String())
	}
	loc := w.Header().Get("Location")
	if !strings.Contains(loc, "ok=") {
		t.Fatalf("Location=%q", loc)
	}
	id, ok := cfg.LookupGitHubIdentity("4242")
	if !ok || id.Login != "bob" || id.Name != "Bob" || id.Email != "bob@example.com" {
		t.Fatalf("lookup: ok=%v %+v", ok, id)
	}
	// Round-trip on disk via same APIs the UI uses
	raw, err := os.ReadFile(cfg.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "bob") || !strings.Contains(string(raw), "4242") {
		t.Fatalf("config.json missing map: %s", raw)
	}
	// Update same id
	form = url.Values{
		"discordUserId": {"4242"},
		"login":         {"bob2"},
		"csrf":          {csrf},
	}
	req = httptest.NewRequest(http.MethodPost, "/config/github-identities", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: sid})
	w = httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusFound && w.Code != http.StatusSeeOther {
		t.Fatalf("update status=%d", w.Code)
	}
	id, ok = cfg.LookupGitHubIdentity("4242")
	if !ok || id.Login != "bob2" {
		t.Fatalf("update: %+v", id)
	}
	// Remove
	form = url.Values{"discordUserId": {"4242"}, "csrf": {csrf}}
	req = httptest.NewRequest(http.MethodPost, "/config/github-identities/remove", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: sid})
	w = httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusFound && w.Code != http.StatusSeeOther {
		t.Fatalf("remove status=%d", w.Code)
	}
	if _, ok := cfg.LookupGitHubIdentity("4242"); ok {
		t.Fatal("still mapped after remove")
	}
	// Audit
	evs, err := srv.audit.ReadDay(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var sawSet, sawRemove bool
	for _, ev := range evs {
		if ev.Action == audit.ActionConfigSetGitHubIdent && ev.OK && ev.Actor == "admin-1" {
			sawSet = true
		}
		if ev.Action == audit.ActionConfigRemoveGitHubIdent && ev.OK && ev.Actor == "admin-1" {
			sawRemove = true
		}
	}
	if !sawSet || !sawRemove {
		t.Fatalf("audit missing set/remove: %+v", evs)
	}
}

func TestSetGitHubIdentityRejectsEmptyLogin(t *testing.T) {
	srv, cfg, _ := authOnServer(t)
	sid, csrf, err := srv.LoginAs("admin-1", "Admin", config.WebRoleAdmin)
	if err != nil {
		t.Fatal(err)
	}
	form := url.Values{
		"discordUserId": {"1"},
		"login":         {"  "},
		"csrf":          {csrf},
	}
	req := httptest.NewRequest(http.MethodPost, "/config/github-identities", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: sid})
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusFound && w.Code != http.StatusSeeOther {
		t.Fatalf("status=%d", w.Code)
	}
	loc := w.Header().Get("Location")
	if !strings.Contains(loc, "err=") {
		t.Fatalf("Location=%q want err", loc)
	}
	if _, ok := cfg.LookupGitHubIdentity("1"); ok {
		t.Fatal("empty login must not map")
	}
}

func TestGitHubIdentityMemberForbidden(t *testing.T) {
	srv, _, _ := authOnServer(t)
	sid, csrf, err := srv.LoginAs("member-1", "M", config.WebRoleMember)
	if err != nil {
		t.Fatal(err)
	}
	form := url.Values{
		"discordUserId": {"1"},
		"login":         {"x"},
		"csrf":          {csrf},
	}
	req := httptest.NewRequest(http.MethodPost, "/config/github-identities", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: sid})
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status=%d want 403", w.Code)
	}
}
