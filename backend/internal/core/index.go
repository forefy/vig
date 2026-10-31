package core

import (
	"fmt"
	"regexp"
	"strings"
)

// An audit's index is a set of Parquet tables built off the job by an
// operator tool (zz) and pushed with the job's token: code symbols, calls
// and flows from the checkout, requests and bodies from captures, cloud
// resources and names from dumps, and the edges between them. Atlas reads
// the parts in the browser; the backend only keeps and serves them.

// IndexPart is one Parquet file of a table.
type IndexPart struct {
	File   string `json:"file" doc:"File name inside the table, as the tool wrote it" example:"part-1761900000-ab12cd34.parquet"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}

// IndexTable is one table of the index and its parts.
type IndexTable struct {
	Name  string      `json:"name" doc:"Table name, lowercase with underscores" example:"calls"`
	Rows  int64       `json:"rows,omitempty" doc:"Row count when the tool reported one"`
	Bytes int64       `json:"bytes,omitempty"`
	Parts []IndexPart `json:"parts"`
}

// IndexManifest is what an operator seals an upload with: the tool and
// every table and part it staged.
type IndexManifest struct {
	Tool        string       `json:"tool" minLength:"1" maxLength:"40" example:"zz"`
	ToolVersion string       `json:"tool_version,omitempty" maxLength:"60"`
	Tables      []IndexTable `json:"tables" minItems:"1" maxItems:"64"`
}

var (
	indexTableRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
	indexFileRe  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}\.parquet$`)
	sha256Re     = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// ValidIndexTable reports whether name can be a table directory.
func ValidIndexTable(name string) bool { return indexTableRe.MatchString(name) }

// ValidIndexFile reports whether name can be a part file. No separators,
// no dot-prefix, always a parquet extension.
func ValidIndexFile(name string) bool {
	return indexFileRe.MatchString(name) && !strings.Contains(name, "..")
}

// ValidSHA256 reports whether s is a lowercase hex digest.
func ValidSHA256(s string) bool { return sha256Re.MatchString(s) }

// Validate checks names and digests and sums bytes per table.
func (m *IndexManifest) Validate() error {
	if len(m.Tables) == 0 {
		return fmt.Errorf("tables: at least one table")
	}
	seen := map[string]bool{}
	for i := range m.Tables {
		t := &m.Tables[i]
		if !ValidIndexTable(t.Name) {
			return fmt.Errorf("tables[%d].name %q: lowercase letters, digits and underscores", i, t.Name)
		}
		if seen[t.Name] {
			return fmt.Errorf("tables[%d].name %q repeats", i, t.Name)
		}
		seen[t.Name] = true
		if len(t.Parts) == 0 {
			return fmt.Errorf("table %s has no parts", t.Name)
		}
		files := map[string]bool{}
		t.Bytes = 0
		for j, p := range t.Parts {
			if !ValidIndexFile(p.File) {
				return fmt.Errorf("%s parts[%d].file %q: a plain .parquet file name", t.Name, j, p.File)
			}
			if files[p.File] {
				return fmt.Errorf("%s parts[%d].file %q repeats", t.Name, j, p.File)
			}
			files[p.File] = true
			if !ValidSHA256(p.SHA256) {
				return fmt.Errorf("%s/%s: sha256 is 64 lowercase hex characters", t.Name, p.File)
			}
			if p.Bytes <= 0 {
				return fmt.Errorf("%s/%s: bytes must be positive", t.Name, p.File)
			}
			t.Bytes += p.Bytes
		}
	}
	return nil
}

// TotalBytes sums every part.
func (m IndexManifest) TotalBytes() int64 {
	var n int64
	for _, t := range m.Tables {
		for _, p := range t.Parts {
			n += p.Bytes
		}
	}
	return n
}
