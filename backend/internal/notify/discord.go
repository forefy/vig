package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

const discordAPI = "https://discord.com/api/v10"

// Discord sends DMs through a bot. The user must share a guild with the bot.
type Discord struct {
	Token string
	HTTP  *http.Client

	mu      sync.Mutex
	members map[string]memberEntry
}

type memberEntry struct {
	at     time.Time
	member bool
}

// NewDiscord builds the sender; an empty token means not configured.
func NewDiscord(token string) *Discord {
	return &Discord{Token: token, HTTP: &http.Client{Timeout: 15 * time.Second}, members: map[string]memberEntry{}}
}

func (d *Discord) Name() string     { return "discord" }
func (d *Discord) Configured() bool { return d != nil && d.Token != "" }

// Send opens a DM channel with the user and posts the text.
func (d *Discord) Send(ctx context.Context, discordUserID, _ string, text string) error {
	if !d.Configured() {
		return ErrNotConfigured
	}
	var ch struct {
		ID string `json:"id"`
	}
	if err := d.call(ctx, http.MethodPost, "/users/@me/channels", map[string]any{"recipient_id": discordUserID}, &ch); err != nil {
		return err
	}
	return d.call(ctx, http.MethodPost, "/channels/"+ch.ID+"/messages", map[string]any{"content": text}, nil)
}

// MemberOf reports whether the Discord user is a member of the guild,
// cached for five minutes per (guild, user). Errors other than 404 are
// returned; a 404 is a definite "not a member".
func (d *Discord) MemberOf(ctx context.Context, guildID, userID string) (bool, error) {
	if !d.Configured() || guildID == "" || userID == "" {
		return false, ErrNotConfigured
	}
	key := guildID + ":" + userID
	d.mu.Lock()
	if e, ok := d.members[key]; ok && time.Since(e.at) < 5*time.Minute {
		d.mu.Unlock()
		return e.member, nil
	}
	d.mu.Unlock()
	err := d.call(ctx, http.MethodGet, "/guilds/"+guildID+"/members/"+userID, nil, nil)
	member := err == nil
	if err != nil {
		var he *httpError
		if !asHTTPError(err, &he) || he.status != http.StatusNotFound {
			return false, err
		}
	}
	d.mu.Lock()
	d.members[key] = memberEntry{at: time.Now(), member: member}
	if len(d.members) > 5000 {
		for k, e := range d.members {
			if time.Since(e.at) >= 5*time.Minute {
				delete(d.members, k)
			}
		}
	}
	d.mu.Unlock()
	return member, nil
}

type httpError struct {
	status int
	msg    string
}

func (e *httpError) Error() string { return e.msg }

func asHTTPError(err error, target **httpError) bool {
	he, ok := err.(*httpError)
	if ok {
		*target = he
	}
	return ok
}

func (d *Discord) call(ctx context.Context, method, path string, body any, out any) error {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, discordAPI+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bot "+d.Token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := d.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return &httpError{status: resp.StatusCode, msg: fmt.Sprintf("discord %s: %s %s", path, resp.Status, bytes.TrimSpace(msg))}
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}
