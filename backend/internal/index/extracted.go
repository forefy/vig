package index

import "time"

// Extracted is what one source file yields.
type Extracted struct {
	Functions []FunctionRow
	Calls     []CallRow
	Flows     []FlowRow
	Imports   []ImportRow
	Literals  []LiteralRow
	Routes    []RouteRow
	// Bindings are routers handed to a call: argument i carries a prefix,
	// or a parameter of the calling function that carries one. The build
	// follows them into the callee's parameters.
	Bindings []Binding
}

// Binding is a router passed as an argument.
type Binding struct {
	CallID   string
	ArgIndex int
	FnID     string // the calling function
	Prefix   string // the prefix known where the call is made
	Param    string // the parameter of FnID the router came from, if any
}

// stamp fills target and time on every row of an extraction.
func (e *Extracted) stamp(target string, at time.Time) {
	for i := range e.Functions {
		e.Functions[i].Target, e.Functions[i].IngestedAt = target, at
	}
	for i := range e.Calls {
		e.Calls[i].Target, e.Calls[i].IngestedAt = target, at
	}
	for i := range e.Flows {
		e.Flows[i].Target, e.Flows[i].IngestedAt = target, at
	}
	for i := range e.Imports {
		e.Imports[i].Target, e.Imports[i].IngestedAt = target, at
	}
	for i := range e.Literals {
		e.Literals[i].Target, e.Literals[i].IngestedAt = target, at
	}
	for i := range e.Routes {
		e.Routes[i].Target, e.Routes[i].IngestedAt = target, at
	}
}
