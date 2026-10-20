// Package rustsrc reads a checked-out Rust workspace once and returns one
// document describing every on-chain program in it: handlers with their
// file and line, their accounts and constraints, the functions each
// handler reaches, and the CPI, PDA, token, lamport and unsafe sites in
// that reachable code. Everything is pattern-based over Anchor and native
// conventions; nothing here parses Rust or needs a toolchain, and nothing
// here imports a chain SDK. Consumers are the pre-flight, the panel and the
// surface map.
package rustsrc

// Model is the whole workspace, program crate by program crate.
type Model struct {
	Crates []Crate `json:"crates"`
	Files  []File  `json:"files"`
	// Functions is every fn found, keyed by "file:name".
	Functions map[string]*Function `json:"-"`
	// Consts is every byte or string constant declared anywhere in the
	// workspace, by name: what PDA seeds are usually spelled with.
	Consts map[string][]byte `json:"-"`
	// Types is every plain struct and enum in the workspace, by name: the
	// shapes of instruction arguments and account data.
	Types map[string]TypeDef `json:"-"`
}

// File is one source file's line accounting.
type File struct {
	Path     string `json:"path"`
	Lines    int    `json:"lines"`
	Code     int    `json:"code" doc:"Lines with code, the nSLOC"`
	Comments int    `json:"comments"`
	Blank    int    `json:"blank"`
	Todos    int    `json:"todos,omitempty" doc:"TODO, FIXME, HACK, XXX markers"`
}

// Crate is one on-chain program crate: a Cargo package whose source
// declares an id, a #[program] module, or an entrypoint.
type Crate struct {
	Name         string        `json:"name" doc:"Cargo package name"`
	Root         string        `json:"root" doc:"Directory holding Cargo.toml, repo-relative"`
	Framework    string        `json:"framework" enum:"anchor,native,unknown"`
	DeclaredIDs  []string      `json:"declared_ids,omitempty"`
	Module       string        `json:"module,omitempty" doc:"The #[program] module name"`
	Instructions []Instruction `json:"instructions"`
	Files        []string      `json:"files"`
	// Consts declared in this crate's own files; the workspace's fill in.
	Consts map[string][]byte `json:"-"`
	// Structs maps an Accounts struct name to its fields, for composites.
	Structs map[string]*AccountsStruct `json:"-"`
}

// Instruction is one entrypoint: an Anchor handler or a native match arm.
type Instruction struct {
	Name    string `json:"name" doc:"snake_case handler name"`
	File    string `json:"file"`
	Line    int    `json:"line"`
	Context string `json:"context,omitempty" doc:"Accounts struct name"`
	// ContextFile is where that struct is declared: the instruction's own
	// module when handlers in lib.rs delegate to one file per instruction.
	ContextFile string    `json:"context_file,omitempty"`
	Args        []Arg     `json:"args,omitempty"`
	Accounts    []Account `json:"accounts"`
	// Reach is every function the handler reaches through resolved calls,
	// the handler first.
	Reach      []string `json:"reach"`
	Unresolved int      `json:"unresolved,omitempty" doc:"Calls that matched no function or more than one"`
	Sites      []Site   `json:"sites,omitempty"`
	Remaining  bool     `json:"remaining,omitempty" doc:"Reads ctx.remaining_accounts"`
	Counts     Counts   `json:"counts"`
}

// Arg is one handler argument after the context.
type Arg struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// Account is one field of an Accounts struct, flattened through composites
// as parent.child like the IDL does.
type Account struct {
	Name     string `json:"name"`
	Wrapper  string `json:"wrapper" doc:"Account, Signer, Program, UncheckedAccount, AccountInfo, AccountLoader, InterfaceAccount, Interface, Sysvar, SystemAccount, Box, Option"`
	Inner    string `json:"inner,omitempty" doc:"The state or program type inside the wrapper"`
	Signer   bool   `json:"signer,omitempty"`
	Writable bool   `json:"writable,omitempty"`
	Raw      bool   `json:"raw,omitempty" doc:"No type or owner check by the framework"`
	Optional bool   `json:"optional,omitempty"`
	Init     bool   `json:"init,omitempty"`
	Close    bool   `json:"close,omitempty"`
	Realloc  bool   `json:"realloc,omitempty"`
	ZeroCopy bool   `json:"zero_copy,omitempty"`
	Seeds    string `json:"seeds,omitempty"`
	// SeedsProgram is the seeds::program clause when the address is derived
	// under another program: a program account's name, or a path to an ID.
	SeedsProgram string   `json:"seeds_program,omitempty"`
	Bump         bool     `json:"bump,omitempty" doc:"A bump constraint: the PDA is checked against the canonical bump"`
	HasOne       []string `json:"has_one,omitempty"`
	Owner        string   `json:"owner,omitempty"`
	Address      string   `json:"address,omitempty"`
	Constraints  int      `json:"constraints,omitempty" doc:"constraint = ... clauses"`
	Line         int      `json:"line,omitempty"`
}

// Site is one place in reachable code that matters to a reviewer.
type Site struct {
	Kind     string `json:"kind" enum:"cpi,pda,token,lamports,unsafe,realloc,serde"`
	Op       string `json:"op,omitempty" doc:"cpi: invoke, invoke_signed, cpi_context, cpi_signer, method; token: transfer, mint_to, ...; pda: find, create, seeds"`
	Target   string `json:"target,omitempty" doc:"Program name for a CPI, when it can be told"`
	Ix       string `json:"ix,omitempty" doc:"The instruction called, for crate::cpi::instruction(...) sites"`
	Signed   bool   `json:"signed,omitempty"`
	Function string `json:"function"`
	File     string `json:"file"`
	Line     int    `json:"line"`
}

// Counts is what the reachable code contains, as numbers.
type Counts struct {
	Functions int `json:"functions"`
	Accounts  int `json:"accounts"`
	Raw       int `json:"raw"`
	CPI       int `json:"cpi"`
	SignedCPI int `json:"signed_cpi"`
	PDA       int `json:"pda"`
	Token     int `json:"token"`
	Lamports  int `json:"lamports"`
	Unsafe    int `json:"unsafe"`
	Realloc   int `json:"realloc"`
	Serde     int `json:"serde"`
}

// Function is one fn item.
type Function struct {
	Name  string `json:"name"`
	File  string `json:"file"`
	Line  int    `json:"line"`
	End   int    `json:"end"`
	Crate string `json:"crate"`
	Impl  string `json:"impl,omitempty" doc:"Enclosing impl type, when any"`
	Calls []Call `json:"calls,omitempty"`
	body  string // masked: strings and comments blanked, for call and site matching
	text  string // clean: comments blanked, strings kept, for fingerprints
}

// Call is one call expression inside a function body.
type Call struct {
	Name   string `json:"name"`
	Path   string `json:"path,omitempty" doc:"Qualifier before the name: token, Self, ctx.accounts.vault"`
	Method bool   `json:"method,omitempty"`
	Line   int    `json:"line"`
}

// AccountsStruct is one #[derive(Accounts)] item.
type AccountsStruct struct {
	Name   string
	File   string
	Line   int
	Fields []Account
	// Composite maps a field name to the Accounts struct it embeds.
	Composite map[string]string
}

// Summary is the workspace in numbers, for a badge strip.
type Summary struct {
	Programs     int `json:"programs"`
	Instructions int `json:"instructions"`
	Accounts     int `json:"accounts"`
	Raw          int `json:"raw"`
	CPI          int `json:"cpi"`
	SignedCPI    int `json:"signed_cpi"`
	PDA          int `json:"pda"`
	Token        int `json:"token"`
	Unsafe       int `json:"unsafe"`
	Code         int `json:"code" doc:"nSLOC across program crates"`
}

// Summarize totals the model.
func (m *Model) Summarize() Summary {
	s := Summary{Programs: len(m.Crates)}
	inCrate := map[string]bool{}
	for _, c := range m.Crates {
		for _, f := range c.Files {
			inCrate[f] = true
		}
		s.Instructions += len(c.Instructions)
		for _, ix := range c.Instructions {
			s.Accounts += ix.Counts.Accounts
			s.Raw += ix.Counts.Raw
			s.CPI += ix.Counts.CPI
			s.SignedCPI += ix.Counts.SignedCPI
			s.PDA += ix.Counts.PDA
			s.Token += ix.Counts.Token
			s.Unsafe += ix.Counts.Unsafe
		}
	}
	for _, f := range m.Files {
		if inCrate[f.Path] {
			s.Code += f.Code
		}
	}
	return s
}

// CrateFor finds the crate that declares a program id, else the only crate
// when there is one. ok is false when the model cannot tell.
func (m *Model) CrateFor(programID string) (*Crate, bool) {
	for i := range m.Crates {
		for _, id := range m.Crates[i].DeclaredIDs {
			if id == programID {
				return &m.Crates[i], true
			}
		}
	}
	if len(m.Crates) == 1 {
		return &m.Crates[0], true
	}
	return nil, false
}
