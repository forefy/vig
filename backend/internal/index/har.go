package index

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// A HAR capture, as browsers and proxies write it.
type harFile struct {
	Log struct {
		Entries []struct {
			Pageref         string  `json:"pageref"`
			StartedDateTime string  `json:"startedDateTime"`
			Time            float64 `json:"time"`
			Request         struct {
				Method  string `json:"method"`
				URL     string `json:"url"`
				Headers []struct {
					Name  string `json:"name"`
					Value string `json:"value"`
				} `json:"headers"`
				Cookies []struct {
					Name string `json:"name"`
				} `json:"cookies"`
				PostData *struct {
					MimeType string `json:"mimeType"`
					Text     string `json:"text"`
				} `json:"postData"`
			} `json:"request"`
			Response struct {
				Status  int `json:"status"`
				Headers []struct {
					Name  string `json:"name"`
					Value string `json:"value"`
				} `json:"headers"`
				Content struct {
					MimeType string `json:"mimeType"`
					Size     int64  `json:"size"`
					Text     string `json:"text"`
					Encoding string `json:"encoding"`
				} `json:"content"`
			} `json:"response"`
		} `json:"entries"`
	} `json:"log"`
}

// Traffic is what captures add to the index.
type Traffic struct {
	Entries []HAREntryRow
	Bodies  []BodyRow
	Hosts   []HostRow
	Edges   []EdgeRow
}

// Headers the index does not keep: credentials and the cookie jar. The
// capture carries them; the index names cookies, never their values.
var secretHeaders = map[string]bool{"authorization": true, "cookie": true, "set-cookie": true, "proxy-authorization": true, "x-api-key": true, "x-auth-token": true}

// ReadHAR reads one capture into traffic rows. The session is the file's
// base name; a body is kept by its hash, and a host that served one gets
// a `serves` edge to it.
func ReadHAR(path, target string, now time.Time) (Traffic, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Traffic{}, err
	}
	var har harFile
	if err := json.Unmarshal(raw, &har); err != nil {
		return Traffic{}, fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	session := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	var t Traffic
	bodies := map[string]BodyRow{}
	var reqTexts, respTexts []string
	hosts := map[string]*HostRow{}
	served := map[string]bool{}
	for _, e := range har.Log.Entries {
		u, err := url.Parse(e.Request.URL)
		if err != nil || u.Host == "" {
			continue
		}
		host := strings.ToLower(u.Hostname())
		at, _ := time.Parse(time.RFC3339Nano, e.StartedDateTime)
		if at.IsZero() {
			at = now
		}
		at = at.UTC()
		sum := sha256.Sum256([]byte(session + "\x00" + e.StartedDateTime + "\x00" + e.Request.Method + "\x00" + e.Request.URL))
		row := HAREntryRow{Target: target, IngestedAt: now, Session: session, EntryID: hex.EncodeToString(sum[:8]), StartedAt: at,
			Method: e.Request.Method, Scheme: u.Scheme, Host: host, Path: u.Path, Query: u.RawQuery, Status: int32(e.Response.Status),
			ReqHeaders: map[string]string{}, RespHeaders: map[string]string{}, CookieNames: []string{}, TimeMs: e.Time, PageRef: e.Pageref}
		for _, h := range e.Request.Headers {
			k := strings.ToLower(h.Name)
			if secretHeaders[k] {
				continue
			}
			if k == "content-type" {
				row.ReqContentType = h.Value
			}
			row.ReqHeaders[k] = h.Value
		}
		for _, h := range e.Response.Headers {
			k := strings.ToLower(h.Name)
			if k == "content-type" {
				row.RespContentType = h.Value
			}
			if k == "set-cookie" {
				if name, _, ok := strings.Cut(h.Value, "="); ok {
					row.CookieNames = append(row.CookieNames, strings.TrimSpace(name))
				}
				continue
			}
			if secretHeaders[k] {
				continue
			}
			row.RespHeaders[k] = h.Value
		}
		for _, c := range e.Request.Cookies {
			row.CookieNames = append(row.CookieNames, c.Name)
		}
		row.CookieNames = uniq(row.CookieNames)
		reqText, respText := "", ""
		if e.Request.PostData != nil && e.Request.PostData.Text != "" {
			text := e.Request.PostData.Text
			reqText = bodyText(text, isText(e.Request.PostData.MimeType))
			s := sha256.Sum256([]byte(text))
			sha := hex.EncodeToString(s[:])
			row.ReqBodySHA256 = sha
			if _, ok := bodies[sha]; !ok {
				mime := e.Request.PostData.MimeType
				bodies[sha] = BodyRow{Target: target, IngestedAt: now, SHA256: sha, Bytes: int64(len(text)), ContentType: mime, IsText: isText(mime), FirstSeen: at, Text: bodyText(text, isText(mime))}
			}
		}
		if e.Response.Content.Text != "" {
			body := []byte(e.Response.Content.Text)
			if e.Response.Content.Encoding == "base64" {
				if dec, err := base64.StdEncoding.DecodeString(e.Response.Content.Text); err == nil {
					body = dec
				}
			}
			s := sha256.Sum256(body)
			sha := hex.EncodeToString(s[:])
			row.RespBodySHA256 = sha
			respText = bodyText(string(body), isText(e.Response.Content.MimeType))
			row.RespBodyBytes = int64(len(body))
			if _, ok := bodies[sha]; !ok {
				mime := e.Response.Content.MimeType
				bodies[sha] = BodyRow{Target: target, IngestedAt: now, SHA256: sha, Bytes: int64(len(body)), ContentType: mime, IsText: isText(mime), FirstSeen: at, Text: bodyText(string(body), isText(mime))}
			}
			if k := host + "\x00" + sha; !served[k] {
				served[k] = true
				t.Edges = append(t.Edges, EdgeRow{Target: target, IngestedAt: now, SrcKind: "host", SrcID: HostPrefix + host, DstKind: "body", DstID: BodyPrefix + sha, Rel: "serves", Confidence: 1, Evidence: u.Path})
			}
		} else if e.Response.Content.Size > 0 {
			row.RespBodyBytes = e.Response.Content.Size
		}
		// The entry is a node: its host requests it, and it returns its body.
		t.Edges = append(t.Edges, EdgeRow{Target: target, IngestedAt: now, SrcKind: "host", SrcID: HostPrefix + host, DstKind: "entry", DstID: EntryPrefix + row.EntryID, Rel: "requests", Confidence: 1, Evidence: e.Request.Method + " " + u.Path})
		if row.RespBodySHA256 != "" {
			t.Edges = append(t.Edges, EdgeRow{Target: target, IngestedAt: now, SrcKind: "entry", SrcID: EntryPrefix + row.EntryID, DstKind: "body", DstID: BodyPrefix + row.RespBodySHA256, Rel: "returns", Confidence: 1, Evidence: strconv.Itoa(e.Response.Status)})
		}
		h := hosts[host]
		if h == nil {
			h = &HostRow{Target: target, IngestedAt: now, Host: host, FirstSeen: at, LastSeen: at, Sources: []string{"har"}}
			hosts[host] = h
		}
		if at.Before(h.FirstSeen) {
			h.FirstSeen = at
		}
		if at.After(h.LastSeen) {
			h.LastSeen = at
		}
		t.Entries = append(t.Entries, row)
		reqTexts = append(reqTexts, reqText)
		respTexts = append(respTexts, respText)
	}
	t.Edges = append(t.Edges, linkTraffic(t.Entries, reqTexts, respTexts, target, now)...)
	for _, b := range bodies {
		t.Bodies = append(t.Bodies, b)
	}
	sort.Slice(t.Bodies, func(i, j int) bool { return t.Bodies[i].SHA256 < t.Bodies[j].SHA256 })
	for _, h := range hosts {
		t.Hosts = append(t.Hosts, *h)
	}
	sort.Slice(t.Hosts, func(i, j int) bool { return t.Hosts[i].Host < t.Hosts[j].Host })
	return t, nil
}

func isText(mime string) bool {
	m := strings.ToLower(mime)
	return strings.HasPrefix(m, "text/") || strings.Contains(m, "json") || strings.Contains(m, "javascript") || strings.Contains(m, "xml") || strings.Contains(m, "html") || strings.Contains(m, "css") || strings.Contains(m, "form")
}

// A body the index keeps the text of is cut here. The links between
// entries are read off the kept text, so what the index says travelled
// is what a reader can see.
const bodyTextMax = 1 << 20

// bodyText is a text body as the index keeps it, or nothing for bytes.
func bodyText(s string, text bool) string {
	if !text {
		return ""
	}
	return clean(s, bodyTextMax)
}

func uniq(in []string) []string {
	seen := map[string]bool{}
	out := in[:0]
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}
