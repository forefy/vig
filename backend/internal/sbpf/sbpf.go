// Package sbpf profiles deployed Solana programs from their bytecode alone.
//
// It is the reverse-engineering half of the intake: when a program has no
// verified build, the profile still says what the code can do (which
// syscalls it imports and therefore whether it does CPI, derives PDAs, logs,
// hashes), which framework built it, which crates and versions are linked,
// which source files and instruction names the compiler left behind, and
// how large it is. Everything here is deterministic and offline: one ELF in,
// one Profile out. Nothing is executed.
package sbpf

import (
	"bytes"
	"debug/elf"
	"encoding/binary"
	"errors"
	"regexp"
	"sort"
	"strings"
)

// Profile is what the bytecode reveals.
type Profile struct {
	Size         int      `json:"size" doc:"ELF size in bytes after trailing zero padding is removed"`
	TextSize     int      `json:"text_size" doc:"Bytes of executable code"`
	RodataSize   int      `json:"rodata_size" doc:"Bytes of read-only data"`
	Instructions int      `json:"instruction_count" doc:"Number of sBPF instructions"`
	Functions    int      `json:"function_count" doc:"Distinct local call targets plus the entrypoint"`
	Syscalls     []string `json:"syscalls" doc:"Imported runtime syscalls"`
	Capabilities []string `json:"capabilities" doc:"What the syscalls imply: cpi, pda, logging, crypto, sysvars, return-data, introspection, heap, compute-meter, panics"`
	Framework    string   `json:"framework" doc:"anchor, pinocchio, native or unknown"`
	AnchorIxs    []string `json:"anchor_instructions,omitempty" doc:"Anchor instruction names recovered from their discriminators"`
	AnchorAccts  []string `json:"anchor_accounts,omitempty" doc:"Anchor account type names recovered from their discriminators"`
	Crates       []Crate  `json:"crates,omitempty" doc:"Crates and versions linked in, from registry paths in panic strings"`
	SourcePaths  []string `json:"source_paths,omitempty" doc:"Project source files named in panic strings"`
	Rustc        string   `json:"rustc,omitempty" doc:"Compiler commit hash from /rustc/<hash>/ paths"`
	Strings      []string `json:"strings,omitempty" doc:"A sample of other readable strings"`
}

// Crate is a linked dependency.
type Crate struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// ErrNotELF means the bytes are not an ELF image.
var ErrNotELF = errors.New("not an ELF image")

const (
	opCall = 0x85
	opLddw = 0x18
)

var (
	reCrate   = regexp.MustCompile(`index\.crates\.io-[0-9a-f]+/([A-Za-z0-9_-]+?)-([0-9]+\.[0-9]+\.[0-9]+(?:[-+][0-9A-Za-z.+-]*)?)/`)
	reRustc   = regexp.MustCompile(`/rustc/([0-9a-f]{40})/`)
	reSource  = regexp.MustCompile(`(?:^|[^A-Za-z0-9_./-])((?:programs?|crates?|src|lib|app|sdk|token|core)/[A-Za-z0-9_./-]*?\.rs)`)
	reGitDeps = regexp.MustCompile(`/git/checkouts/([A-Za-z0-9_-]+?)-[0-9a-f]+/([0-9a-f]{7,})/`)
)

// Analyze profiles one program image.
func Analyze(data []byte) (Profile, error) {
	p := Profile{Size: len(bytes.TrimRight(data, "\x00")), Framework: "unknown"}
	if len(data) < 4 || string(data[:4]) != "\x7fELF" {
		return p, ErrNotELF
	}
	// Deployed images are zero padded and parse as-is. An image that was
	// trimmed (a hash normalisation, a cached copy) may have lost the zero
	// tail of its section header table; pad it back before giving up.
	f, err := elf.NewFile(bytes.NewReader(data))
	if err != nil {
		padded := make([]byte, len(data)+4096)
		copy(padded, data)
		if f, err = elf.NewFile(bytes.NewReader(padded)); err != nil {
			return p, err
		}
	}
	defer f.Close()

	var text, rodata []byte
	for _, s := range f.Sections {
		switch {
		case s.Name == ".text":
			text, _ = s.Data()
		case s.Name == ".rodata" || strings.HasPrefix(s.Name, ".rodata."):
			b, _ := s.Data()
			rodata = append(rodata, b...)
		}
	}
	p.TextSize = len(text)
	p.RodataSize = len(rodata)

	// Imports: modern toolchains emit undefined dynamic symbols for syscalls.
	imported := map[string]bool{}
	if syms, err := f.DynamicSymbols(); err == nil {
		for _, s := range syms {
			if s.Section == elf.SHN_UNDEF && s.Name != "" {
				imported[s.Name] = true
			}
		}
	}

	// Code: count instructions, local calls and hashed syscalls.
	targets := map[int32]bool{}
	hashed := map[uint32]bool{}
	for off := 0; off+8 <= len(text); {
		op := text[off]
		src := text[off+1] >> 4
		imm := int32(binary.LittleEndian.Uint32(text[off+4 : off+8]))
		p.Instructions++
		if op == opCall {
			switch src {
			case 1: // pc-relative local call
				targets[int32(off/8)+1+imm] = true
			case 0: // syscall by murmur3 hash (older toolchains) or a relocated import
				hashed[uint32(imm)] = true
			}
		}
		if op == opLddw {
			off += 16
			p.Instructions++ // lddw occupies two slots
			continue
		}
		off += 8
	}
	p.Functions = len(targets) + 1
	for name, h := range syscallHashes {
		if hashed[h] {
			imported[name] = true
		}
	}
	for name := range imported {
		p.Syscalls = append(p.Syscalls, name)
	}
	sort.Strings(p.Syscalls)
	p.Capabilities = capabilities(p.Syscalls)

	// Strings: what the compiler could not strip.
	seenCrate := map[string]bool{}
	seenSrc := map[string]bool{}
	corpus := rodata
	if len(corpus) == 0 {
		corpus = data
	}
	for _, s := range readable(corpus, 6) {
		for _, m := range reCrate.FindAllStringSubmatch(s, -1) {
			key := m[1] + "@" + m[2]
			if !seenCrate[key] {
				seenCrate[key] = true
				p.Crates = append(p.Crates, Crate{Name: m[1], Version: m[2]})
			}
		}
		for _, m := range reGitDeps.FindAllStringSubmatch(s, -1) {
			key := m[1] + "@git"
			if !seenCrate[key] {
				seenCrate[key] = true
				p.Crates = append(p.Crates, Crate{Name: m[1], Version: "git:" + m[2][:7]})
			}
		}
		if p.Rustc == "" {
			if m := reRustc.FindStringSubmatch(s); m != nil {
				p.Rustc = m[1]
			}
		}
		if !strings.Contains(s, "index.crates.io") && !strings.Contains(s, "/rustc/") && !strings.Contains(s, "/git/checkouts/") {
			for _, m := range reSource.FindAllStringSubmatch(s, -1) {
				path := m[1]
				if !seenSrc[path] && len(seenSrc) < 60 {
					seenSrc[path] = true
					p.SourcePaths = append(p.SourcePaths, path)
				}
			}
		}
	}
	p.AnchorIxs, p.AnchorAccts = findDiscriminators(corpus, text)
	sort.Strings(p.AnchorIxs)
	sort.Strings(p.AnchorAccts)
	sort.Slice(p.Crates, func(i, j int) bool { return p.Crates[i].Name < p.Crates[j].Name })
	sort.Strings(p.SourcePaths)
	p.Framework = framework(p)
	p.Strings = sample(readable(corpus, 12), 40)
	return p, nil
}

// framework infers how the program was written.
func framework(p Profile) string {
	for _, c := range p.Crates {
		switch c.Name {
		case "anchor-lang":
			return "anchor"
		case "pinocchio":
			return "pinocchio"
		}
	}
	if len(p.AnchorIxs) > 0 || len(p.AnchorAccts) > 0 {
		return "anchor"
	}
	for _, c := range p.Crates {
		if c.Name == "solana-program" || c.Name == "solana-program-entrypoint" {
			return "native"
		}
	}
	if len(p.Syscalls) > 0 {
		return "native"
	}
	return "unknown"
}

// capabilities maps syscalls to what a reviewer cares about.
func capabilities(syscalls []string) []string {
	caps := map[string]bool{}
	for _, s := range syscalls {
		switch {
		case strings.HasPrefix(s, "sol_invoke_signed"):
			caps["cpi"] = true
		case s == "sol_create_program_address" || s == "sol_try_find_program_address":
			caps["pda"] = true
		case strings.HasPrefix(s, "sol_log"):
			caps["logging"] = true
		case s == "sol_set_return_data" || s == "sol_get_return_data":
			caps["return-data"] = true
		case strings.HasPrefix(s, "sol_get_") && strings.HasSuffix(s, "_sysvar"), s == "sol_get_sysvar", s == "sol_get_last_restart_slot", s == "sol_get_epoch_stake":
			caps["sysvars"] = true
		case s == "sol_sha256", s == "sol_keccak256", s == "sol_blake3", s == "sol_poseidon", s == "sol_secp256k1_recover",
			strings.HasPrefix(s, "sol_curve_"), strings.HasPrefix(s, "sol_alt_bn128"), s == "sol_big_mod_exp":
			caps["crypto"] = true
		case s == "sol_get_processed_sibling_instruction" || s == "sol_get_stack_height":
			caps["introspection"] = true
		case s == "sol_alloc_free_":
			caps["heap"] = true
		case s == "sol_remaining_compute_units":
			caps["compute-meter"] = true
		case s == "sol_panic_" || s == "abort":
			caps["panics"] = true
		}
	}
	out := make([]string, 0, len(caps))
	for c := range caps {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}

// readable extracts printable ASCII runs of at least min bytes.
func readable(b []byte, min int) []string {
	var out []string
	start := -1
	for i, c := range b {
		printable := c >= 0x20 && c < 0x7f
		if printable && start < 0 {
			start = i
		}
		if !printable && start >= 0 {
			if i-start >= min {
				out = append(out, string(b[start:i]))
			}
			start = -1
		}
	}
	if start >= 0 && len(b)-start >= min {
		out = append(out, string(b[start:]))
	}
	return out
}

// sample keeps the strings a reviewer would read first: no paths, no
// panic-formatting fragments, deduplicated, at most n.
func sample(strs []string, n int) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range strs {
		s = strings.TrimSpace(s)
		switch {
		case len(s) < 12 || len(s) > 120:
			continue
		case strings.Contains(s, "/"), strings.Contains(s, "\\"):
			continue
		case strings.HasPrefix(s, "called `"), strings.Contains(s, "unwrap()"), strings.Contains(s, "index out of bounds"):
			continue
		case seen[s]:
			continue
		}
		seen[s] = true
		out = append(out, s)
		if len(out) == n {
			break
		}
	}
	return out
}
