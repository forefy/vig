package core

import (
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// A reference that points at lines of a GitHub repository is captured when
// the report is stored, the way the located lines of the pinned repository
// are: the lines come into the report, and the link is rewritten to the
// commit it resolved to, so the report never depends on a branch moving.

// BlobLink is a parsed github.com/owner/name/blob/<rev>/<path>#L<a>-L<b> link.
type BlobLink struct {
	Owner string
	Name  string
	Rev   string
	Path  string
	Start int
	End   int
}

var blobLinkRE = regexp.MustCompile(`^https?://(?:www\.)?github\.com/([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+)/blob/([^/\s#?]+)/([^#?\s]+)$`)
var lineAnchorRE = regexp.MustCompile(`^L(\d+)(?:-L(\d+))?$`)

// ParseBlobLink reads a GitHub file link with a line anchor. A link with no
// lines, or to anything but a file, is not a blob link: it stays a plain
// reference.
func ParseBlobLink(raw string) (BlobLink, bool) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Fragment == "" {
		return BlobLink{}, false
	}
	m := blobLinkRE.FindStringSubmatch(u.Scheme + "://" + u.Host + u.Path)
	if m == nil {
		return BlobLink{}, false
	}
	a := lineAnchorRE.FindStringSubmatch(u.Fragment)
	if a == nil {
		return BlobLink{}, false
	}
	start, _ := strconv.Atoi(a[1])
	end := start
	if a[2] != "" {
		end, _ = strconv.Atoi(a[2])
	}
	if start < 1 || end < start {
		return BlobLink{}, false
	}
	path, err := url.PathUnescape(m[4])
	if err != nil || strings.Contains(path, "..") {
		return BlobLink{}, false
	}
	return BlobLink{Owner: m[1], Name: m[2], Rev: m[3], Path: path, Start: start, End: end}, true
}

// Pinned is the same link at a full commit.
func (l BlobLink) Pinned(sha string) string {
	anchor := "#L" + strconv.Itoa(l.Start)
	if l.End != l.Start {
		anchor += "-L" + strconv.Itoa(l.End)
	}
	return "https://github.com/" + l.Owner + "/" + l.Name + "/blob/" + sha + "/" + l.Path + anchor
}

// IsSHA says whether the link's revision already is a full commit.
func (l BlobLink) IsSHA() bool {
	if len(l.Rev) != 40 {
		return false
	}
	for _, r := range l.Rev {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

// ReferenceCode is the code a reference link points at, captured when the
// report was stored. url is the link as the author wrote it; pinned is the
// same link at the commit the lines were read from.
type ReferenceCode struct {
	URL    string  `json:"url"`
	Pinned string  `json:"pinned"`
	Repo   string  `json:"repo" doc:"owner/name"`
	Commit string  `json:"commit"`
	File   string  `json:"file"`
	Start  int     `json:"start"`
	End    int     `json:"end"`
	Code   Snippet `json:"code"`
}
