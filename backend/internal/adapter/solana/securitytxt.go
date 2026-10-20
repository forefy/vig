package solana

import (
	"bytes"
	"strings"

	"github.com/forefy/vig/backend/internal/core"
)

// security.txt (solana-security-txt) is a block the build embeds in the
// program image: a start marker, then NUL-separated key/value pairs, then
// an end marker. The deployer writes it, nobody checks it; source_code is
// a claim, not a verified build.
var (
	securityTxtBegin = []byte("=======BEGIN SECURITY.TXT V1=======")
	securityTxtEnd   = []byte("=======END SECURITY.TXT V1=======")
)

// SecurityTxt returns the embedded security.txt fields, nil when absent.
func SecurityTxt(image []byte) map[string]string {
	i := bytes.Index(image, securityTxtBegin)
	if i < 0 {
		return nil
	}
	body := image[i+len(securityTxtBegin):]
	j := bytes.Index(body, securityTxtEnd)
	if j < 0 {
		return nil
	}
	var parts [][]byte
	for _, p := range bytes.Split(body[:j], []byte{0}) {
		if len(p) > 0 {
			parts = append(parts, p)
		}
	}
	out := map[string]string{}
	for k := 0; k+1 < len(parts); k += 2 {
		out[string(parts[k])] = string(parts[k+1])
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// Disclosure lifts the security.txt fields into the chain-neutral shape.
// A bare "None" in auditors is the convention for "no audit"; it is dropped.
func Disclosure(st map[string]string) *core.Disclosure {
	if len(st) == 0 {
		return nil
	}
	d := &core.Disclosure{Name: st["name"], ProjectURL: st["project_url"], SourceURL: st["source_code"], SourceRelease: st["source_release"], Policy: st["policy"], Expiry: st["expiry"]}
	d.Contacts = list(st["contacts"])
	for _, a := range list(st["auditors"]) {
		if !strings.EqualFold(a, "none") {
			d.Auditors = append(d.Auditors, a)
		}
	}
	return d
}

func list(v string) []string {
	var out []string
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
