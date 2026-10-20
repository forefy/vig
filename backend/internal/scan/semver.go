package scan

import (
	"errors"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// Version is a Cargo semver triple. Pre-release tags sort before the release.
type Version struct {
	Major, Minor, Patch int
	Pre                 string
}

var versionRE = regexp.MustCompile(`^v?(\d+)(?:\.(\d+))?(?:\.(\d+))?(?:-([0-9A-Za-z.-]+))?(?:\+[0-9A-Za-z.-]+)?$`)

// ParseVersion parses "1.2.3", "1.2", "1" and pre-release forms.
func ParseVersion(s string) (Version, error) {
	m := versionRE.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return Version{}, errors.New("bad version " + s)
	}
	v := Version{Pre: m[4]}
	v.Major, _ = strconv.Atoi(m[1])
	if m[2] != "" {
		v.Minor, _ = strconv.Atoi(m[2])
	}
	if m[3] != "" {
		v.Patch, _ = strconv.Atoi(m[3])
	}
	return v, nil
}

// Compare orders two versions.
func Compare(a, b Version) int {
	for _, d := range []int{a.Major - b.Major, a.Minor - b.Minor, a.Patch - b.Patch} {
		if d < 0 {
			return -1
		}
		if d > 0 {
			return 1
		}
	}
	switch {
	case a.Pre == b.Pre:
		return 0
	case a.Pre == "":
		return 1
	case b.Pre == "":
		return -1
	}
	return strings.Compare(a.Pre, b.Pre)
}

// MatchReq evaluates a Cargo requirement such as ">= 1.2.0, < 2.0.0",
// "^0.3", "~1.4", "= 2.0.1" or "*" against v. Bare versions are caret.
func MatchReq(req string, v Version) bool {
	req = strings.TrimSpace(req)
	if req == "" || req == "*" {
		return true
	}
	for _, part := range strings.Split(req, ",") {
		if !matchOne(strings.TrimSpace(part), v) {
			return false
		}
	}
	return true
}

func matchOne(part string, v Version) bool {
	op := "^"
	for _, o := range []string{">=", "<=", ">", "<", "=", "^", "~"} {
		if strings.HasPrefix(part, o) {
			op = o
			part = strings.TrimSpace(strings.TrimPrefix(part, o))
			break
		}
	}
	if part == "*" {
		return true
	}
	m := versionRE.FindStringSubmatch(part)
	if m == nil {
		return false
	}
	base, err := ParseVersion(part)
	if err != nil {
		return false
	}
	hasMinor, hasPatch := m[2] != "", m[3] != ""
	c := Compare(v, base)
	switch op {
	case ">=":
		return c >= 0
	case ">":
		return c > 0
	case "<=":
		return c <= 0
	case "<":
		return c < 0
	case "=":
		if !hasMinor {
			return v.Major == base.Major
		}
		if !hasPatch {
			return v.Major == base.Major && v.Minor == base.Minor
		}
		return c == 0
	case "~":
		if c < 0 {
			return false
		}
		if !hasMinor {
			return v.Major == base.Major
		}
		return v.Major == base.Major && v.Minor == base.Minor
	}
	// caret
	if c < 0 {
		return false
	}
	switch {
	case base.Major > 0 || !hasMinor:
		return v.Major == base.Major
	case base.Minor > 0 || !hasPatch:
		return v.Major == base.Major && v.Minor == base.Minor
	default:
		return c == 0
	}
}

// Package is one Cargo.lock entry.
type Package struct {
	Name    string
	Version string
}

// ParseCargoLock reads [[package]] name and version pairs.
func ParseCargoLock(content string) []Package {
	var out []Package
	var cur *Package
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case line == "[[package]]":
			if cur != nil && cur.Name != "" {
				out = append(out, *cur)
			}
			cur = &Package{}
		case cur != nil && strings.HasPrefix(line, "name = "):
			cur.Name = strings.Trim(strings.TrimPrefix(line, "name = "), `"`)
		case cur != nil && strings.HasPrefix(line, "version = "):
			cur.Version = strings.Trim(strings.TrimPrefix(line, "version = "), `"`)
		}
	}
	if cur != nil && cur.Name != "" {
		out = append(out, *cur)
	}
	return out
}

// ReadCargoLock loads and parses a lockfile, returning nil when absent.
func ReadCargoLock(path string) []Package {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return ParseCargoLock(string(b))
}
