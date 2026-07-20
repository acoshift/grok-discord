package web

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// DiscordUser is the identity returned by Discord's /users/@me.
type DiscordUser struct {
	ID         string `json:"id"`
	Username   string `json:"username"`
	GlobalName string `json:"global_name"`
	Avatar     string `json:"avatar"` // hash; empty when using default avatar
}

// DisplayName prefers global_name, then username.
func (u DiscordUser) DisplayName() string {
	if g := strings.TrimSpace(u.GlobalName); g != "" {
		return g
	}
	return strings.TrimSpace(u.Username)
}

// AvatarURL is the Discord CDN URL for this user's profile image.
// Custom avatars use the hash; otherwise the default embed avatar for the user id.
func (u DiscordUser) AvatarURL() string {
	return discordAvatarURL(u.ID, u.Avatar)
}

// discordAvatarURL builds a CDN URL from a Discord user id and optional avatar hash.
func discordAvatarURL(userID, avatarHash string) string {
	userID = strings.TrimSpace(userID)
	avatarHash = strings.TrimSpace(avatarHash)
	if userID == "" {
		return ""
	}
	if avatarHash != "" {
		ext := "png"
		if strings.HasPrefix(avatarHash, "a_") {
			ext = "gif"
		}
		return fmt.Sprintf("https://cdn.discordapp.com/avatars/%s/%s.%s?size=64", userID, avatarHash, ext)
	}
	// Default avatar index for the modern username system: (snowflake >> 22) % 6.
	id, err := strconv.ParseUint(userID, 10, 64)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("https://cdn.discordapp.com/embed/avatars/%d.png", (id>>22)%6)
}

// DiscordOAuth exchanges codes and fetches the current user.
// Tests inject fakes; production uses HTTPDiscordOAuth.
type DiscordOAuth interface {
	ExchangeCode(ctx context.Context, code, redirectURI, clientID, clientSecret string) (accessToken string, err error)
	FetchUser(ctx context.Context, accessToken string) (DiscordUser, error)
}

// HTTPDiscordOAuth talks to discord.com.
type HTTPDiscordOAuth struct {
	HTTPClient *http.Client
}

func (h *HTTPDiscordOAuth) client() *http.Client {
	if h != nil && h.HTTPClient != nil {
		return h.HTTPClient
	}
	return &http.Client{Timeout: 15 * time.Second}
}

func (h *HTTPDiscordOAuth) ExchangeCode(ctx context.Context, code, redirectURI, clientID, clientSecret string) (string, error) {
	form := url.Values{}
	form.Set("client_id", clientID)
	form.Set("client_secret", clientSecret)
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", redirectURI)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://discord.com/api/oauth2/token", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := h.client().Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("discord token exchange: status %d: %s", res.StatusCode, strings.TrimSpace(string(body)))
	}
	var tok struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(body, &tok); err != nil {
		return "", fmt.Errorf("discord token json: %w", err)
	}
	if tok.AccessToken == "" {
		return "", fmt.Errorf("discord token exchange: empty access_token")
	}
	return tok.AccessToken, nil
}

func (h *HTTPDiscordOAuth) FetchUser(ctx context.Context, accessToken string) (DiscordUser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://discord.com/api/users/@me", nil)
	if err != nil {
		return DiscordUser{}, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	res, err := h.client().Do(req)
	if err != nil {
		return DiscordUser{}, err
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode != http.StatusOK {
		return DiscordUser{}, fmt.Errorf("discord @me: status %d: %s", res.StatusCode, strings.TrimSpace(string(body)))
	}
	var u DiscordUser
	if err := json.Unmarshal(body, &u); err != nil {
		return DiscordUser{}, fmt.Errorf("discord @me json: %w", err)
	}
	if strings.TrimSpace(u.ID) == "" {
		return DiscordUser{}, fmt.Errorf("discord @me: empty id")
	}
	return u, nil
}

// FakeDiscordOAuth is a test double for OAuth exchange + @me.
type FakeDiscordOAuth struct {
	// CodeToUser maps authorization code → user (token step is simulated).
	CodeToUser map[string]DiscordUser
	// FailExchange / FailUser force errors.
	FailExchange error
	FailUser     error
}

func (f *FakeDiscordOAuth) ExchangeCode(_ context.Context, code, _, _, _ string) (string, error) {
	if f.FailExchange != nil {
		return "", f.FailExchange
	}
	if f.CodeToUser == nil {
		return "", fmt.Errorf("unknown code")
	}
	if _, ok := f.CodeToUser[code]; !ok {
		return "", fmt.Errorf("unknown code %q", code)
	}
	return "fake-access-" + code, nil
}

func (f *FakeDiscordOAuth) FetchUser(_ context.Context, accessToken string) (DiscordUser, error) {
	if f.FailUser != nil {
		return DiscordUser{}, f.FailUser
	}
	code := strings.TrimPrefix(accessToken, "fake-access-")
	if f.CodeToUser == nil {
		return DiscordUser{}, fmt.Errorf("no users")
	}
	u, ok := f.CodeToUser[code]
	if !ok {
		return DiscordUser{}, fmt.Errorf("unknown token")
	}
	return u, nil
}

func discordAuthorizeURL(clientID, redirectURI, state string) string {
	q := url.Values{}
	q.Set("client_id", clientID)
	q.Set("response_type", "code")
	q.Set("scope", "identify")
	q.Set("redirect_uri", redirectURI)
	q.Set("state", state)
	return "https://discord.com/api/oauth2/authorize?" + q.Encode()
}
