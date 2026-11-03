// Package index builds an audit's index: Parquet tables an operator makes
// from the job's checkout and captures and pushes under the job's token,
// read in the browser by the Atlas page. Code comes from the source
// through tree-sitter, traffic from HAR captures, names and resources
// from listings; the edges table joins them. The tables are the ones the
// frontend's questions are written against, so an index built here and
// one built by hand read the same.
package index

import "time"

// Tool is what the manifest records as the builder.
const Tool = "vig"

// Node id prefixes, shared with the questions and the graph.
const (
	FnPrefix        = "fn:"
	VarPrefix       = "var:"
	CallPrefix      = "call:"
	RetPrefix       = "ret:"
	HostPrefix      = "host:"
	SubdomainPrefix = "subdomain:"
	ResourcePrefix  = "resource:"
	BodyPrefix      = "body:"
	RepoPrefix      = "repo:"
	LiteralPrefix   = "literal:"
	EntryPrefix     = "entry:"
)

// RepoRow is one repository the index covers, at the commit it was read.
type RepoRow struct {
	Target        string    `parquet:"target"`
	IngestedAt    time.Time `parquet:"ingested_at,timestamp(millisecond)"`
	Repo          string    `parquet:"repo"`
	Commit        string    `parquet:"commit"`
	DefaultBranch string    `parquet:"default_branch"`
	Language      string    `parquet:"language"`
	SizeKB        int32     `parquet:"size_kb"`
	FetchedAt     time.Time `parquet:"fetched_at,timestamp(millisecond)"`
}

// FileRow is one source file the extractor read.
type FileRow struct {
	Target     string    `parquet:"target"`
	IngestedAt time.Time `parquet:"ingested_at,timestamp(millisecond)"`
	Repo       string    `parquet:"repo"`
	Path       string    `parquet:"path"`
	Lang       string    `parquet:"lang"`
	Bytes      int64     `parquet:"bytes"`
	SHA256     string    `parquet:"sha256"`
	LOC        int64     `parquet:"loc"`
}

// FunctionRow is one function or method. FnID is `repo:path:name:line`,
// the id the map and the questions carry as `fn:` + FnID.
type FunctionRow struct {
	Target     string    `parquet:"target"`
	IngestedAt time.Time `parquet:"ingested_at,timestamp(millisecond)"`
	Repo       string    `parquet:"repo"`
	Path       string    `parquet:"path"`
	FnID       string    `parquet:"fn_id"`
	Name       string    `parquet:"name"`
	Qualname   string    `parquet:"qualname"`
	Params     []string  `parquet:"params,list"`
	LineStart  int64     `parquet:"line_start"`
	LineEnd    int64     `parquet:"line_end"`
	IsExported bool      `parquet:"is_exported"`
	IsHandler  bool      `parquet:"is_handler"`
}

// CallRow is one call expression. CalleeFn is set when the callee resolved
// to a function of the same repository.
type CallRow struct {
	Target     string    `parquet:"target"`
	IngestedAt time.Time `parquet:"ingested_at,timestamp(millisecond)"`
	Repo       string    `parquet:"repo"`
	Path       string    `parquet:"path"`
	CallID     string    `parquet:"call_id"`
	CallerFn   string    `parquet:"caller_fn,optional"`
	CalleeName string    `parquet:"callee_name"`
	CalleeFn   string    `parquet:"callee_fn,optional"`
	Resolution string    `parquet:"resolution"`
	Args       []string  `parquet:"args,list"`
	ArgKinds   []string  `parquet:"arg_kinds,list"`
	Line       int64     `parquet:"line"`
	Col        int64     `parquet:"col"`
}

// FlowRow is one step a value takes inside a function: a variable into a
// variable (assign), a variable into a call's argument (arg, with its
// index) or receiver (recv), a call's result into a variable (assign), a
// variable or call into the function's return (return), and across a
// resolved call, an argument into the callee's parameter (param) and the
// callee's return back into the call (return).
type FlowRow struct {
	Target     string    `parquet:"target"`
	IngestedAt time.Time `parquet:"ingested_at,timestamp(millisecond)"`
	Repo       string    `parquet:"repo"`
	Path       string    `parquet:"path"`
	FnID       string    `parquet:"fn_id,optional"`
	Src        string    `parquet:"src"`
	Dst        string    `parquet:"dst"`
	Kind       string    `parquet:"kind"`
	ArgIndex   int64     `parquet:"arg_index"`
	Line       int64     `parquet:"line"`
}

// ImportRow is one imported module or name.
type ImportRow struct {
	Target     string    `parquet:"target"`
	IngestedAt time.Time `parquet:"ingested_at,timestamp(millisecond)"`
	Repo       string    `parquet:"repo"`
	Path       string    `parquet:"path"`
	Module     string    `parquet:"module"`
	Name       string    `parquet:"name,optional"`
	Alias      string    `parquet:"alias,optional"`
	Line       int64     `parquet:"line"`
}

// LiteralRow is one string literal: a url, a host, a path or any string.
type LiteralRow struct {
	Target     string    `parquet:"target"`
	IngestedAt time.Time `parquet:"ingested_at,timestamp(millisecond)"`
	Repo       string    `parquet:"repo"`
	Path       string    `parquet:"path"`
	Value      string    `parquet:"value"`
	Kind       string    `parquet:"kind"`
	Line       int64     `parquet:"line"`
}

// RouteRow is one route registration: the pattern a framework binds and
// the handler it names, resolved to a function id when it could be.
type RouteRow struct {
	Target     string    `parquet:"target"`
	IngestedAt time.Time `parquet:"ingested_at,timestamp(millisecond)"`
	Repo       string    `parquet:"repo"`
	Path       string    `parquet:"path"`
	Method     string    `parquet:"method,optional"`
	Pattern    string    `parquet:"pattern"`
	Handler    string    `parquet:"handler,optional"`
	HandlerFn  string    `parquet:"handler_fn,optional"`
	Framework  string    `parquet:"framework"`
	Line       int64     `parquet:"line"`
	// FnID is the function the registration sits in, Router the variable
	// it is made on: `router.HandleFunc(...)` inside `mount(router)`.
	FnID   string `parquet:"fn_id,optional"`
	Router string `parquet:"router,optional"`
	// routerParam names the parameter of FnID the router came in through,
	// when it did; the build then puts the caller's prefix in front.
	routerParam string
}

// HostRow is one host seen in traffic or named in a listing.
type HostRow struct {
	Target     string    `parquet:"target"`
	IngestedAt time.Time `parquet:"ingested_at,timestamp(millisecond)"`
	Host       string    `parquet:"host"`
	FirstSeen  time.Time `parquet:"first_seen,timestamp(millisecond)"`
	LastSeen   time.Time `parquet:"last_seen,timestamp(millisecond)"`
	Sources    []string  `parquet:"sources,list"`
}

// HAREntryRow is one request of a capture.
type HAREntryRow struct {
	Target          string            `parquet:"target"`
	IngestedAt      time.Time         `parquet:"ingested_at,timestamp(millisecond)"`
	Session         string            `parquet:"session"`
	EntryID         string            `parquet:"entry_id"`
	StartedAt       time.Time         `parquet:"started_at,timestamp(millisecond)"`
	Method          string            `parquet:"method"`
	Scheme          string            `parquet:"scheme"`
	Host            string            `parquet:"host"`
	Path            string            `parquet:"path"`
	Query           string            `parquet:"query,optional"`
	Status          int32             `parquet:"status"`
	ReqContentType  string            `parquet:"req_content_type,optional"`
	RespContentType string            `parquet:"resp_content_type,optional"`
	ReqHeaders      map[string]string `parquet:"req_headers"`
	RespHeaders     map[string]string `parquet:"resp_headers"`
	CookieNames     []string          `parquet:"cookie_names,list"`
	ReqBodySHA256   string            `parquet:"req_body_sha256,optional"`
	RespBodySHA256  string            `parquet:"resp_body_sha256,optional"`
	RespBodyBytes   int64             `parquet:"resp_body_bytes"`
	TimeMs          float64           `parquet:"time_ms"`
	PageRef         string            `parquet:"page_ref,optional"`
}

// BodyRow is one response body a capture carried, by its hash.
type BodyRow struct {
	Target      string    `parquet:"target"`
	IngestedAt  time.Time `parquet:"ingested_at,timestamp(millisecond)"`
	SHA256      string    `parquet:"sha256"`
	Bytes       int64     `parquet:"bytes"`
	ContentType string    `parquet:"content_type,optional"`
	IsText      bool      `parquet:"is_text"`
	FirstSeen   time.Time `parquet:"first_seen,timestamp(millisecond)"`
	BlobPack    string    `parquet:"blob_pack,optional"`
	// The body itself when it is text, up to 1 MB, so a reader sees what
	// a request carried and what came back.
	Text string `parquet:"text,optional"`
}

// SubdomainRow is one name from a DNS listing.
type SubdomainRow struct {
	Target     string    `parquet:"target"`
	IngestedAt time.Time `parquet:"ingested_at,timestamp(millisecond)"`
	Apex       string    `parquet:"apex"`
	FQDN       string    `parquet:"fqdn"`
	A          []string  `parquet:"a,list"`
	CNAME      string    `parquet:"cname,optional"`
	Source     string    `parquet:"source"`
	FirstSeen  time.Time `parquet:"first_seen,timestamp(millisecond)"`
	LastSeen   time.Time `parquet:"last_seen,timestamp(millisecond)"`
}

// CloudResourceRow is one resource from a cloud listing.
type CloudResourceRow struct {
	Target     string            `parquet:"target"`
	IngestedAt time.Time         `parquet:"ingested_at,timestamp(millisecond)"`
	Provider   string            `parquet:"provider"`
	AccountID  string            `parquet:"account_id,optional"`
	Region     string            `parquet:"region,optional"`
	Service    string            `parquet:"service,optional"`
	RType      string            `parquet:"rtype,optional"`
	RID        string            `parquet:"rid"`
	Name       string            `parquet:"name,optional"`
	Public     bool              `parquet:"public"`
	Hostnames  []string          `parquet:"hostnames,list"`
	Tags       map[string]string `parquet:"tags"`
	Snapshot   time.Time         `parquet:"snapshot,timestamp(millisecond)"`
}

// EdgeRow is one relation between two nodes of the index.
type EdgeRow struct {
	Target     string    `parquet:"target"`
	IngestedAt time.Time `parquet:"ingested_at,timestamp(millisecond)"`
	SrcKind    string    `parquet:"src_kind"`
	SrcID      string    `parquet:"src_id"`
	DstKind    string    `parquet:"dst_kind"`
	DstID      string    `parquet:"dst_id"`
	Rel        string    `parquet:"rel"`
	Confidence float64   `parquet:"confidence"`
	Evidence   string    `parquet:"evidence,optional"`
}

// A line of a source file the build read, so a reader can see the place a
// function, a call or a flow names. Written in repository, path and line
// order in small row groups, so DuckDB reads one file's lines without the
// rest.
type SourceRow struct {
	Target     string    `parquet:"target"`
	IngestedAt time.Time `parquet:"ingested_at,timestamp(millisecond)"`
	Repo       string    `parquet:"repo"`
	Path       string    `parquet:"path"`
	Line       int64     `parquet:"line"`
	Text       string    `parquet:"text"`
}
