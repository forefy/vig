//go:build cgo

package index

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
	"unicode"

	ts "github.com/tree-sitter/go-tree-sitter"
	tsc "github.com/tree-sitter/tree-sitter-c/bindings/go"
	tscpp "github.com/tree-sitter/tree-sitter-cpp/bindings/go"
	tsgo "github.com/tree-sitter/tree-sitter-go/bindings/go"
	tsjs "github.com/tree-sitter/tree-sitter-javascript/bindings/go"
	tspy "github.com/tree-sitter/tree-sitter-python/bindings/go"
	tsrs "github.com/tree-sitter/tree-sitter-rust/bindings/go"
	tsts "github.com/tree-sitter/tree-sitter-typescript/bindings/go"
)

// CanExtract is true in a build that carries the parsers.
const CanExtract = true

var (
	langOnce  sync.Once
	languages map[Lang]*ts.Language
)

func language(l Lang) *ts.Language {
	langOnce.Do(func() {
		languages = map[Lang]*ts.Language{
			LangGo:         ts.NewLanguage(tsgo.Language()),
			LangJavaScript: ts.NewLanguage(tsjs.Language()),
			LangTypeScript: ts.NewLanguage(tsts.LanguageTypescript()),
			LangTSX:        ts.NewLanguage(tsts.LanguageTSX()),
			LangPython:     ts.NewLanguage(tspy.Language()),
			LangRust:       ts.NewLanguage(tsrs.Language()),
			LangC:          ts.NewLanguage(tsc.Language()),
			LangCPP:        ts.NewLanguage(tscpp.Language()),
		}
	})
	return languages[l]
}

// Extract parses one file and pulls its functions, calls, flows, imports,
// literals and route registrations. Rows carry the repository and path;
// the build fills target and ingested_at.
func Extract(lang Lang, repo, path string, src []byte) (Extracted, error) {
	l := language(lang)
	if l == nil {
		return Extracted{}, fmt.Errorf("no parser for %q", lang)
	}
	p := ts.NewParser()
	defer p.Close()
	if err := p.SetLanguage(l); err != nil {
		return Extracted{}, err
	}
	tree := p.Parse(src, nil)
	if tree == nil {
		return Extracted{}, fmt.Errorf("%s: parse failed", path)
	}
	defer tree.Close()
	x := &extractor{lang: lang, repo: repo, path: path, src: src, handlerAt: map[uint]bool{}, prefixes: map[string]prefixInfo{}}
	x.walk(tree.RootNode())
	return x.out, nil
}

type extractor struct {
	lang       Lang
	repo, path string
	src        []byte
	out        Extracted
	fnStack    []string
	// handlerAt marks function nodes (by start byte) a route registration
	// passed as its handler, so the function row says is_handler.
	handlerAt map[uint]bool
	// prefixes holds the path a router variable carries: a gorilla
	// subrouter made with PathPrefix, a gin or echo group. A registration
	// on that variable gets the prefix in front of its pattern. A router
	// rooted at a parameter of the function names it, so the build can
	// put the caller's prefix in front as well.
	prefixes map[string]prefixInfo
	// params are the parameter names of the enclosing functions.
	params [][]string
}

type prefixInfo struct {
	prefix string
	param  string
}

// ---- the walk ----

func (x *extractor) walk(n *ts.Node) {
	kind := n.Kind()
	switch {
	case x.isFunc(kind):
		row := x.function(n)
		x.out.Functions = append(x.out.Functions, row)
		x.fnStack = append(x.fnStack, row.FnID)
		x.params = append(x.params, paramNames(row.Params))
		x.children(n)
		x.fnStack = x.fnStack[:len(x.fnStack)-1]
		x.params = x.params[:len(x.params)-1]
		return
	case x.isCall(kind):
		x.call(n)
	case x.assignFields(kind) != nil:
		x.assign(n)
	case x.isReturn(kind):
		x.ret(n)
	case x.isString(kind):
		x.literal(n)
		return
	case x.isImport(kind):
		x.imp(n)
		return
	}
	x.children(n)
}

func (x *extractor) children(n *ts.Node) {
	c := n.Walk()
	kids := n.NamedChildren(c)
	c.Close()
	for i := range kids {
		x.walk(&kids[i])
	}
}

func (x *extractor) text(n *ts.Node) string { return clean(n.Utf8Text(x.src), 0) }

func (x *extractor) flat(n *ts.Node) string { return strings.Join(strings.Fields(x.text(n)), " ") }

func line(n *ts.Node) int64 { return int64(n.StartPosition().Row) + 1 }

func col(n *ts.Node) int64 { return int64(n.StartPosition().Column) + 1 }

func (x *extractor) fnIDOrEmpty() string {
	if len(x.fnStack) > 0 {
		return x.fnStack[len(x.fnStack)-1]
	}
	return ""
}

func (x *extractor) scope() string {
	if len(x.fnStack) > 0 {
		return x.fnStack[len(x.fnStack)-1]
	}
	return x.repo + ":" + x.path
}

func (x *extractor) varID(name string) string { return VarPrefix + x.scope() + "#" + name }

// callID names a call by where its argument list opens: chained calls
// start at the same column, their parentheses never do.
func (x *extractor) callID(n *ts.Node) string {
	at := n.StartPosition()
	if c := x.calleeNode(n); c != nil {
		at = c.EndPosition()
	}
	return CallPrefix + fmt.Sprintf("%s:%s:%d:%d", x.repo, x.path, at.Row+1, at.Column+1)
}

func (x *extractor) fnID(name string, n *ts.Node) string {
	return fmt.Sprintf("%s:%s:%s:%d", x.repo, x.path, name, line(n))
}

// ---- node kinds per language ----

func (x *extractor) isFunc(kind string) bool {
	switch x.lang {
	case LangGo:
		return kind == "function_declaration" || kind == "method_declaration" || kind == "func_literal"
	case LangJavaScript, LangTypeScript, LangTSX:
		switch kind {
		case "function_declaration", "generator_function_declaration", "method_definition", "arrow_function", "function_expression", "function", "generator_function":
			return true
		}
	case LangPython:
		return kind == "function_definition"
	case LangRust:
		return kind == "function_item"
	case LangC, LangCPP:
		return kind == "function_definition"
	}
	return false
}

func (x *extractor) isCall(kind string) bool {
	switch x.lang {
	case LangJavaScript, LangTypeScript, LangTSX:
		return kind == "call_expression" || kind == "new_expression"
	case LangPython:
		return kind == "call"
	case LangRust:
		return kind == "call_expression" || kind == "macro_invocation"
	}
	return kind == "call_expression"
}

func (x *extractor) isString(kind string) bool {
	switch kind {
	case "interpreted_string_literal", "raw_string_literal", "string", "template_string", "string_literal":
		return true
	}
	return false
}

func (x *extractor) isImport(kind string) bool {
	switch x.lang {
	case LangGo:
		return kind == "import_spec"
	case LangJavaScript, LangTypeScript, LangTSX:
		return kind == "import_statement"
	case LangPython:
		return kind == "import_statement" || kind == "import_from_statement"
	case LangRust:
		return kind == "use_declaration"
	case LangC, LangCPP:
		return kind == "preproc_include"
	}
	return false
}

func (x *extractor) isReturn(kind string) bool {
	if x.lang == LangRust {
		return kind == "return_expression"
	}
	return kind == "return_statement"
}

// assignFields names the target and value fields of an assignment kind.
func (x *extractor) assignFields(kind string) []string {
	switch x.lang {
	case LangGo:
		switch kind {
		case "short_var_declaration", "assignment_statement":
			return []string{"left", "right"}
		case "var_spec", "const_spec":
			return []string{"name", "value"}
		}
	case LangJavaScript, LangTypeScript, LangTSX:
		switch kind {
		case "variable_declarator":
			return []string{"name", "value"}
		case "assignment_expression", "augmented_assignment_expression":
			return []string{"left", "right"}
		}
	case LangPython:
		switch kind {
		case "assignment", "augmented_assignment":
			return []string{"left", "right"}
		}
	case LangRust:
		switch kind {
		case "let_declaration":
			return []string{"pattern", "value"}
		case "assignment_expression", "compound_assignment_expr":
			return []string{"left", "right"}
		}
	case LangC, LangCPP:
		switch kind {
		case "init_declarator":
			return []string{"declarator", "value"}
		case "assignment_expression":
			return []string{"left", "right"}
		}
	}
	return nil
}

// ---- functions ----

func (x *extractor) function(n *ts.Node) FunctionRow {
	name, qual := x.funcName(n)
	if name == "" {
		name = "<anon>"
	}
	if qual == "" {
		qual = name
	}
	params := x.funcParams(n)
	row := FunctionRow{Repo: x.repo, Path: x.path, FnID: x.fnID(name, n), Name: name, Qualname: qual, Params: params,
		LineStart: line(n), LineEnd: int64(n.EndPosition().Row) + 1, IsExported: x.exported(name, n)}
	row.IsHandler = x.handlerAt[n.StartByte()] || x.handlerBySignature(name, params, n)
	if r, ok := x.decoratedRoute(n, row.FnID); ok {
		x.out.Routes = append(x.out.Routes, r)
		row.IsHandler = true
	}
	return row
}

func (x *extractor) funcName(n *ts.Node) (name, qual string) {
	switch x.lang {
	case LangGo:
		if nm := n.ChildByFieldName("name"); nm != nil {
			name = x.text(nm)
		}
		if n.Kind() == "method_declaration" {
			if recv := n.ChildByFieldName("receiver"); recv != nil {
				t := strings.TrimLeft(x.typeOfParam(recv), "*")
				if t != "" {
					qual = t + "." + name
				}
			}
		}
	case LangJavaScript, LangTypeScript, LangTSX:
		switch n.Kind() {
		case "function_declaration", "generator_function_declaration", "method_definition":
			if nm := n.ChildByFieldName("name"); nm != nil {
				name = x.text(nm)
			}
		default:
			name = x.ownerName(n)
		}
		if cls := x.className(n); cls != "" && name != "" {
			qual = cls + "." + name
		}
	case LangPython:
		if nm := n.ChildByFieldName("name"); nm != nil {
			name = x.text(nm)
		}
		if cls := x.className(n); cls != "" {
			qual = cls + "." + name
		}
	case LangRust:
		if nm := n.ChildByFieldName("name"); nm != nil {
			name = x.text(nm)
		}
		for p := n.Parent(); p != nil; p = p.Parent() {
			if p.Kind() == "impl_item" || p.Kind() == "trait_item" {
				if t := p.ChildByFieldName("type"); t != nil {
					qual = x.flat(t) + "." + name
				} else if t := p.ChildByFieldName("name"); t != nil {
					qual = x.flat(t) + "." + name
				}
				break
			}
		}
	case LangC, LangCPP:
		d := n.ChildByFieldName("declarator")
		for d != nil {
			switch d.Kind() {
			case "pointer_declarator", "reference_declarator", "function_declarator", "parenthesized_declarator", "attributed_declarator":
				d = d.ChildByFieldName("declarator")
				if d == nil {
					break
				}
				continue
			}
			break
		}
		if d != nil {
			full := x.flat(d)
			name = full
			if i := strings.LastIndex(full, "::"); i >= 0 {
				name = full[i+2:]
				qual = full
			}
		}
	}
	return name, qual
}

// ownerName names an anonymous function by what holds it: the variable,
// the property or the field it is assigned to.
func (x *extractor) ownerName(n *ts.Node) string {
	p := n.Parent()
	if p == nil {
		return ""
	}
	switch p.Kind() {
	case "variable_declarator", "public_field_definition", "field_definition", "property_signature":
		if nm := p.ChildByFieldName("name"); nm != nil {
			return x.text(nm)
		}
	case "pair":
		if k := p.ChildByFieldName("key"); k != nil {
			return unquote(x.text(k))
		}
	case "assignment_expression":
		if l := p.ChildByFieldName("left"); l != nil {
			t := x.flat(l)
			if i := strings.LastIndexAny(t, "."); i >= 0 {
				return t[i+1:]
			}
			return t
		}
	case "export_statement":
		return "default"
	case "parenthesized_expression", "await_expression", "as_expression", "satisfies_expression":
		return x.ownerName(p)
	}
	return ""
}

func (x *extractor) className(n *ts.Node) string {
	for p := n.Parent(); p != nil; p = p.Parent() {
		switch p.Kind() {
		case "class_declaration", "class", "class_expression", "abstract_class_declaration", "class_definition":
			if nm := p.ChildByFieldName("name"); nm != nil {
				return x.text(nm)
			}
			return ""
		case "function_declaration", "function_definition", "method_definition":
			return ""
		}
	}
	return ""
}

// typeOfParam is the type text of the first parameter in a list.
func (x *extractor) typeOfParam(list *ts.Node) string {
	c := list.Walk()
	kids := list.NamedChildren(c)
	c.Close()
	for i := range kids {
		if t := kids[i].ChildByFieldName("type"); t != nil {
			return x.flat(t)
		}
	}
	return ""
}

func (x *extractor) paramList(n *ts.Node) *ts.Node {
	if x.lang == LangC || x.lang == LangCPP {
		d := n.ChildByFieldName("declarator")
		for d != nil && d.Kind() != "function_declarator" {
			d = d.ChildByFieldName("declarator")
		}
		if d == nil {
			return nil
		}
		return d.ChildByFieldName("parameters")
	}
	return n.ChildByFieldName("parameters")
}

func (x *extractor) funcParams(n *ts.Node) []string {
	list := x.paramList(n)
	if list == nil {
		return nil
	}
	c := list.Walk()
	kids := list.NamedChildren(c)
	c.Close()
	var out []string
	for i := range kids {
		if kids[i].Kind() == "comment" {
			continue
		}
		out = append(out, clean(x.flat(&kids[i]), 80))
		if len(out) == 12 {
			break
		}
	}
	return out
}

func (x *extractor) exported(name string, n *ts.Node) bool {
	switch x.lang {
	case LangGo:
		r := []rune(name)
		return len(r) > 0 && unicode.IsUpper(r[0])
	case LangJavaScript, LangTypeScript, LangTSX:
		if name == "default" {
			return true
		}
		for p, i := n.Parent(), 0; p != nil && i < 3; p, i = p.Parent(), i+1 {
			if p.Kind() == "export_statement" {
				return true
			}
		}
		return false
	case LangPython:
		return !strings.HasPrefix(name, "_")
	case LangRust:
		c := n.Walk()
		kids := n.NamedChildren(c)
		c.Close()
		for i := range kids {
			if kids[i].Kind() == "visibility_modifier" {
				return true
			}
		}
		return false
	case LangC, LangCPP:
		c := n.Walk()
		kids := n.NamedChildren(c)
		c.Close()
		for i := range kids {
			if kids[i].Kind() == "storage_class_specifier" && x.text(&kids[i]) == "static" {
				return false
			}
		}
		return true
	}
	return false
}

var handlerNames = map[string]bool{"GET": true, "POST": true, "PUT": true, "DELETE": true, "PATCH": true, "HEAD": true, "OPTIONS": true, "handler": true, "onRequest": true, "loader": true, "action": true}

// handlerBySignature says whether a function receives a request by the
// shape of its parameters, the way each framework hands one in.
func (x *extractor) handlerBySignature(name string, params []string, n *ts.Node) bool {
	all := strings.Join(params, " ")
	switch x.lang {
	case LangGo:
		if strings.Contains(all, "http.ResponseWriter") && strings.Contains(all, "http.Request") {
			return true
		}
		for _, t := range []string{"gin.Context", "echo.Context", "fiber.Ctx", "fasthttp.RequestCtx", "iris.Context"} {
			if strings.Contains(all, t) {
				return true
			}
		}
	case LangJavaScript, LangTypeScript, LangTSX:
		if handlerNames[name] {
			return true
		}
		names := paramNames(params)
		if len(names) == 0 {
			return false
		}
		first := strings.ToLower(names[0])
		if first == "req" || first == "request" {
			if len(names) == 1 {
				return true
			}
			switch strings.ToLower(names[1]) {
			case "res", "response", "reply", "next", "ctx":
				return true
			}
		}
		if first == "event" && len(names) > 1 && strings.ToLower(names[1]) == "context" {
			return true
		}
	case LangPython:
		names := paramNames(params)
		for _, nm := range names {
			if nm == "self" || nm == "cls" {
				continue
			}
			return nm == "request" || nm == "req"
		}
		for _, d := range x.decorators(n) {
			if strings.Contains(d, "api_view") || strings.Contains(d, "csrf_exempt") || strings.Contains(d, "require_http_methods") || strings.Contains(d, "login_required") {
				return true
			}
		}
	}
	return false
}

// decorators are the decorator texts over a Python function.
func (x *extractor) decorators(n *ts.Node) []string {
	p := n.Parent()
	if p == nil || p.Kind() != "decorated_definition" {
		return nil
	}
	c := p.Walk()
	kids := p.NamedChildren(c)
	c.Close()
	var out []string
	for i := range kids {
		if kids[i].Kind() == "decorator" {
			out = append(out, x.flat(&kids[i]))
		}
	}
	return out
}

var (
	pyRouteRe   = regexp.MustCompile(`@([\w.]+)\.(route|get|post|put|delete|patch|api_route|websocket|head|options)\(\s*(['"])([^'"]*)['"]`)
	pyMethodsRe = regexp.MustCompile(`methods\s*=\s*[\[(]([^\])]*)[\])]`)
	rsRouteRe   = regexp.MustCompile(`#\[\s*(?:[\w:]+::)?(get|post|put|delete|patch|head|options|route)\s*\(\s*"([^"]*)"`)
	rsMethodRe  = regexp.MustCompile(`method\s*=\s*"(\w+)"`)
)

// decoratedRoute reads a route bound by a decorator or an attribute on
// the function itself.
func (x *extractor) decoratedRoute(n *ts.Node, fnID string) (RouteRow, bool) {
	switch x.lang {
	case LangPython:
		for _, d := range x.decorators(n) {
			m := pyRouteRe.FindStringSubmatch(d)
			if m == nil {
				continue
			}
			method := strings.ToUpper(m[2])
			if method == "ROUTE" || method == "API_ROUTE" {
				method = ""
				if mm := pyMethodsRe.FindStringSubmatch(d); mm != nil {
					method = strings.ToUpper(strings.Join(strings.Fields(strings.NewReplacer("'", "", "\"", "", ",", " ").Replace(mm[1])), ","))
				}
			}
			fw := "flask"
			if strings.Contains(m[1], "router") || m[2] == "api_route" {
				fw = "fastapi"
			}
			return RouteRow{Repo: x.repo, Path: x.path, Method: method, Pattern: m[4], Handler: x.flat(n.ChildByFieldName("name")), HandlerFn: fnID, Framework: fw, Line: line(n)}, true
		}
	case LangRust:
		for s := n.PrevNamedSibling(); s != nil && s.Kind() == "attribute_item"; s = s.PrevNamedSibling() {
			t := x.flat(s)
			m := rsRouteRe.FindStringSubmatch(t)
			if m == nil {
				continue
			}
			method := strings.ToUpper(m[1])
			if method == "ROUTE" {
				method = ""
				if mm := rsMethodRe.FindStringSubmatch(t); mm != nil {
					method = strings.ToUpper(mm[1])
				}
			}
			return RouteRow{Repo: x.repo, Path: x.path, Method: method, Pattern: m[2], Handler: x.flat(n.ChildByFieldName("name")), HandlerFn: fnID, Framework: "actix", Line: line(n)}, true
		}
	}
	return RouteRow{}, false
}

// ---- calls ----

func (x *extractor) calleeNode(n *ts.Node) *ts.Node {
	switch n.Kind() {
	case "new_expression":
		return n.ChildByFieldName("constructor")
	case "macro_invocation":
		return n.ChildByFieldName("macro")
	}
	return n.ChildByFieldName("function")
}

func (x *extractor) argNodes(n *ts.Node) []ts.Node {
	a := n.ChildByFieldName("arguments")
	if a == nil {
		return nil
	}
	c := a.Walk()
	kids := a.NamedChildren(c)
	c.Close()
	out := kids[:0]
	for i := range kids {
		if kids[i].Kind() == "comment" {
			continue
		}
		out = append(out, kids[i])
	}
	return out
}

func (x *extractor) argKind(n *ts.Node) string {
	k := n.Kind()
	switch {
	case x.isString(k):
		return "string"
	case strings.Contains(k, "number") || k == "integer" || k == "float" || k == "int_literal" || k == "float_literal" || k == "number_literal":
		return "number"
	case k == "identifier":
		return "identifier"
	case x.isCall(k):
		return "call"
	case x.isFunc(k):
		return "function"
	case k == "true" || k == "false" || k == "nil" || k == "null" || k == "none":
		return "literal"
	}
	return "expression"
}

func (x *extractor) call(n *ts.Node) {
	callee := x.calleeNode(n)
	if callee == nil {
		return
	}
	name := x.flat(callee)
	if n.Kind() == "macro_invocation" {
		name += "!"
	}
	name = clean(name, 160)
	args := x.argNodes(n)
	id := x.callID(n)
	row := CallRow{Repo: x.repo, Path: x.path, CallID: strings.TrimPrefix(id, CallPrefix), CalleeName: name, Resolution: "unresolved", Line: line(n), Col: col(n)}
	if len(x.fnStack) > 0 {
		row.CallerFn = x.fnStack[len(x.fnStack)-1]
	}
	for i := range args {
		a := &args[i]
		row.Args = append(row.Args, clean(x.flat(a), 120))
		row.ArgKinds = append(row.ArgKinds, x.argKind(a))
		x.flowsInto(a, id, "arg", int64(i), n)
		if info, ok := x.routerOf(a); ok {
			x.out.Bindings = append(x.out.Bindings, Binding{CallID: strings.TrimPrefix(id, CallPrefix), ArgIndex: i, FnID: x.fnIDOrEmpty(), Prefix: info.prefix, Param: info.param})
		}
	}
	// What the call is made on carries into it: `r.URL.Query()` reads r.
	if callee.Kind() != "identifier" {
		x.flowsInto(callee, id, "recv", -1, n)
	}
	x.out.Calls = append(x.out.Calls, row)
	x.route(n, name, args)
	x.requireImport(n, name, args)
}

// flowsInto records every variable and call inside node as flowing into
// dst. Nested functions keep their own scope and are not entered.
func (x *extractor) flowsInto(node *ts.Node, dst, kind string, argIndex int64, at *ts.Node) {
	for _, id := range x.idents(node) {
		x.flow(x.varID(id), dst, kind, argIndex, at)
	}
	for _, c := range x.callsIn(node) {
		if cid := x.callID(c); cid != dst {
			x.flow(cid, dst, kind, argIndex, at)
		}
	}
}

func (x *extractor) flow(src, dst, kind string, argIndex int64, at *ts.Node) {
	if src == dst {
		return
	}
	row := FlowRow{Repo: x.repo, Path: x.path, Src: src, Dst: dst, Kind: kind, ArgIndex: argIndex, Line: line(at)}
	if len(x.fnStack) > 0 {
		row.FnID = x.fnStack[len(x.fnStack)-1]
	}
	x.out.Flows = append(x.out.Flows, row)
}

// idents returns the variable names read or written inside a node: plain
// identifiers, leaving out attribute and path segments and anything inside
// a nested function.
func (x *extractor) idents(n *ts.Node) []string {
	seen := map[string]bool{}
	var out []string
	var rec func(m *ts.Node)
	rec = func(m *ts.Node) {
		k := m.Kind()
		if k == "identifier" {
			if x.isVarPosition(m) {
				t := x.text(m)
				if !seen[t] {
					seen[t] = true
					out = append(out, t)
				}
			}
			return
		}
		if m != n && x.isFunc(k) {
			return
		}
		c := m.Walk()
		kids := m.NamedChildren(c)
		c.Close()
		for i := range kids {
			rec(&kids[i])
		}
	}
	rec(n)
	return out
}

// isVarPosition rules out identifiers that name a member or a path
// segment rather than a value: `request.args` reads request, not args.
func (x *extractor) isVarPosition(m *ts.Node) bool {
	p := m.Parent()
	if p == nil {
		return true
	}
	switch p.Kind() {
	case "attribute":
		return !isField(p, "attribute", m)
	case "keyword_argument":
		return !isField(p, "name", m)
	case "scoped_identifier", "scoped_type_identifier", "use_declaration", "use_list", "scoped_use_list", "qualified_identifier", "namespace_identifier":
		return false
	case "field_expression":
		return !isField(p, "field", m)
	}
	return true
}

func isField(parent *ts.Node, field string, m *ts.Node) bool {
	f := parent.ChildByFieldName(field)
	return f != nil && f.StartByte() == m.StartByte() && f.EndByte() == m.EndByte()
}

// callsIn returns the calls inside a node, nested functions excluded.
func (x *extractor) callsIn(n *ts.Node) []*ts.Node {
	var out []*ts.Node
	var rec func(m *ts.Node)
	rec = func(m *ts.Node) {
		k := m.Kind()
		if m != n && x.isCall(k) {
			c := *m
			out = append(out, &c)
		}
		if m != n && x.isFunc(k) {
			return
		}
		c := m.Walk()
		kids := m.NamedChildren(c)
		c.Close()
		for i := range kids {
			rec(&kids[i])
		}
	}
	rec(n)
	return out
}

// ---- assignments and returns ----

var groupRe = regexp.MustCompile("\\.(PathPrefix|Group)\\(\\s*(\"[^\"]*\"|`[^`]*`)")

// isParam says whether a name is a parameter of the enclosing function.
func (x *extractor) isParam(name string) bool {
	if len(x.params) == 0 {
		return false
	}
	for _, p := range x.params[len(x.params)-1] {
		if p == name {
			return true
		}
	}
	return false
}

// routerOf reads what a router expression carries: a known router
// variable, a parameter the router came in through, or a subrouter or
// group made from one of those: `r.PathPrefix("/api/v1").Subrouter()`,
// `e.Group("/v1")`.
func (x *extractor) routerOf(n *ts.Node) (prefixInfo, bool) {
	if n.Kind() == "identifier" {
		name := x.text(n)
		if info, ok := x.prefixes[name]; ok {
			return info, true
		}
		if x.isParam(name) {
			return prefixInfo{param: name}, true
		}
		return prefixInfo{}, false
	}
	t := x.flat(n)
	m := groupRe.FindStringSubmatch(t)
	sub := strings.Contains(t, ".Subrouter()") || strings.Contains(t, ".NewRoute()")
	if m == nil && !sub {
		return prefixInfo{}, false
	}
	var info prefixInfo
	if ids := x.idents(n); len(ids) > 0 {
		if base, ok := x.prefixes[ids[0]]; ok {
			info = base
		} else if x.isParam(ids[0]) {
			info = prefixInfo{param: ids[0]}
		}
	}
	if m != nil {
		info.prefix += unquote(m[2])
	}
	return info, true
}

// routerPrefix records the path a router variable carries when it is made
// from another router.
func (x *extractor) routerPrefix(target string, rhs *ts.Node) {
	if rhs.Kind() == "identifier" {
		return
	}
	if info, ok := x.routerOf(rhs); ok {
		x.prefixes[target] = info
	}
}

func (x *extractor) assign(n *ts.Node) {
	fields := x.assignFields(n.Kind())
	lhs := n.ChildByFieldName(fields[0])
	rhs := n.ChildByFieldName(fields[1])
	if lhs == nil || rhs == nil {
		return
	}
	targets := x.idents(lhs)
	if len(targets) == 0 {
		return
	}
	if len(targets) == 1 {
		x.routerPrefix(targets[0], rhs)
	}
	srcs := x.idents(rhs)
	calls := x.callsIn(rhs)
	if x.isCall(rhs.Kind()) {
		calls = append(calls, rhs)
	}
	for _, t := range targets {
		dst := x.varID(t)
		for _, s := range srcs {
			x.flow(x.varID(s), dst, "assign", -1, n)
		}
		for _, c := range calls {
			x.flow(x.callID(c), dst, "assign", -1, n)
		}
	}
}

func (x *extractor) ret(n *ts.Node) {
	if len(x.fnStack) == 0 {
		return
	}
	dst := RetPrefix + x.fnStack[len(x.fnStack)-1]
	for _, s := range x.idents(n) {
		x.flow(x.varID(s), dst, "return", -1, n)
	}
	for _, c := range x.callsIn(n) {
		x.flow(x.callID(c), dst, "return", -1, n)
	}
}

// ---- literals ----

var (
	hostLikeRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*(\.[a-z0-9][a-z0-9-]*)*\.([a-z]{2,12})$`)
	quoteRe    = regexp.MustCompile("^[a-zA-Z]{0,3}(\"\"\"|'''|\"|'|`)")
)

// The last label a host in code ends with: the public suffixes seen in
// source and the reserved ones a test or a private network uses. A dotted
// lowercase name ending in anything else (`config.db.host`) is a key.
var hostTLDs = map[string]bool{}

func init() {
	for _, t := range strings.Fields(`com net org io dev ai app cloud co me edu gov mil int info xyz biz tech site online store shop blog news page link live run digital systems services network solutions global email zone team today world works tools codes sh gg to ly cc tv so st fm am
		us uk de fr jp cn ru br in au ca nl se no fi dk ch at it es pl cz ie nz za kr sg hk tw eu be pt gr hu ro ua il tr mx ar cl id my th vn ph
		test example local localhost internal invalid lan home corp cluster svc`) {
		hostTLDs[t] = true
	}
}

// hostLike says whether a literal reads as a host: lowercase labels and a
// last label hosts end with.
func hostLike(v string) bool {
	m := hostLikeRe.FindStringSubmatch(v)
	return m != nil && hostTLDs[m[len(m)-1]]
}

// unquote strips the quotes and any prefix letters off a string literal.
func unquote(s string) string {
	m := quoteRe.FindStringSubmatch(s)
	if m == nil {
		return s
	}
	q := m[1]
	s = s[len(m[0]):]
	s = strings.TrimSuffix(s, q)
	return s
}

func literalKind(v string) string {
	lower := strings.ToLower(v)
	switch {
	case strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") || strings.HasPrefix(lower, "ws://") || strings.HasPrefix(lower, "wss://"):
		return "url"
	case len(v) < 254 && hostLike(v):
		return "host"
	case strings.HasPrefix(v, "/") && len(v) > 1 && !strings.ContainsAny(v, " \n\t"):
		return "path"
	}
	return "string"
}

func (x *extractor) literal(n *ts.Node) {
	v := unquote(x.text(n))
	if len(v) < 3 {
		return
	}
	v = clean(v, 200)
	x.out.Literals = append(x.out.Literals, LiteralRow{Repo: x.repo, Path: x.path, Value: v, Kind: literalKind(v), Line: line(n)})
}

// ---- imports ----

func (x *extractor) imp(n *ts.Node) {
	add := func(module, name, alias string) {
		if module == "" {
			return
		}
		x.out.Imports = append(x.out.Imports, ImportRow{Repo: x.repo, Path: x.path, Module: module, Name: name, Alias: alias, Line: line(n)})
	}
	switch x.lang {
	case LangGo:
		if p := n.ChildByFieldName("path"); p != nil {
			alias := ""
			if nm := n.ChildByFieldName("name"); nm != nil {
				alias = x.text(nm)
			}
			add(unquote(x.text(p)), "", alias)
		}
	case LangJavaScript, LangTypeScript, LangTSX:
		src := n.ChildByFieldName("source")
		if src == nil {
			return
		}
		module := unquote(x.text(src))
		added := false
		c := n.Walk()
		kids := n.NamedChildren(c)
		c.Close()
		for i := range kids {
			if kids[i].Kind() != "import_clause" {
				continue
			}
			cc := kids[i].Walk()
			parts := kids[i].NamedChildren(cc)
			cc.Close()
			for j := range parts {
				p := &parts[j]
				switch p.Kind() {
				case "identifier":
					add(module, "default", x.text(p))
					added = true
				case "namespace_import":
					alias := ""
					if a := p.NamedChild(0); a != nil {
						alias = x.text(a)
					}
					add(module, "*", alias)
					added = true
				case "named_imports":
					pc := p.Walk()
					specs := p.NamedChildren(pc)
					pc.Close()
					for k := range specs {
						if specs[k].Kind() != "import_specifier" {
							continue
						}
						name, alias := "", ""
						if nm := specs[k].ChildByFieldName("name"); nm != nil {
							name = x.text(nm)
						}
						if al := specs[k].ChildByFieldName("alias"); al != nil {
							alias = x.text(al)
						}
						add(module, name, alias)
						added = true
					}
				}
			}
		}
		if !added {
			add(module, "", "")
		}
	case LangPython:
		if n.Kind() == "import_statement" {
			c := n.Walk()
			kids := n.NamedChildren(c)
			c.Close()
			for i := range kids {
				module, alias := x.pyName(&kids[i])
				add(module, "", alias)
			}
			return
		}
		module := ""
		if m := n.ChildByFieldName("module_name"); m != nil {
			module = x.flat(m)
		}
		c := n.Walk()
		kids := n.NamedChildren(c)
		c.Close()
		added := false
		for i := range kids {
			k := &kids[i]
			if isField(n, "module_name", k) {
				continue
			}
			switch k.Kind() {
			case "dotted_name", "aliased_import":
				name, alias := x.pyName(k)
				add(module, name, alias)
				added = true
			case "wildcard_import":
				add(module, "*", "")
				added = true
			}
		}
		if !added {
			add(module, "", "")
		}
	case LangRust:
		if a := n.ChildByFieldName("argument"); a != nil {
			add(x.flat(a), "", "")
		}
	case LangC, LangCPP:
		if p := n.ChildByFieldName("path"); p != nil {
			add(strings.Trim(x.text(p), `<>"`), "", "")
		}
	}
}

func (x *extractor) pyName(n *ts.Node) (name, alias string) {
	if n.Kind() == "aliased_import" {
		if nm := n.ChildByFieldName("name"); nm != nil {
			name = x.flat(nm)
		}
		if al := n.ChildByFieldName("alias"); al != nil {
			alias = x.text(al)
		}
		return name, alias
	}
	return x.flat(n), ""
}

// requireImport records a CommonJS require as an import.
func (x *extractor) requireImport(n *ts.Node, callee string, args []ts.Node) {
	if (x.lang != LangJavaScript && x.lang != LangTypeScript && x.lang != LangTSX) || callee != "require" || len(args) != 1 || !x.isString(args[0].Kind()) {
		return
	}
	alias := x.ownerName(n)
	x.out.Imports = append(x.out.Imports, ImportRow{Repo: x.repo, Path: x.path, Module: unquote(x.text(&args[0])), Name: "", Alias: alias, Line: line(n)})
}

// ---- routes ----

var goRouteVerbs = map[string]string{
	"HandleFunc": "", "Handle": "", "Path": "", "PathPrefix": "", "Any": "", "Route": "", "Group": "",
	"Get": "GET", "GET": "GET", "Post": "POST", "POST": "POST", "Put": "PUT", "PUT": "PUT", "Delete": "DELETE", "DELETE": "DELETE",
	"Patch": "PATCH", "PATCH": "PATCH", "Head": "HEAD", "HEAD": "HEAD", "Options": "OPTIONS", "OPTIONS": "OPTIONS",
}

var jsRouteVerbs = map[string]string{"get": "GET", "post": "POST", "put": "PUT", "delete": "DELETE", "patch": "PATCH", "all": "", "use": "", "options": "OPTIONS", "head": "HEAD"}

var rsRouteVerbs = map[string]string{"get": "GET", "post": "POST", "put": "PUT", "delete": "DELETE", "patch": "PATCH", "head": "HEAD", "options": "OPTIONS", "any": ""}

// route reads a registration call: the pattern it binds and the handler
// it names. A handler passed inline is marked, so its own row says so.
func (x *extractor) route(n *ts.Node, callee string, args []ts.Node) {
	simple := simpleName(callee)
	hasDot := strings.ContainsAny(callee, ".:")
	switch x.lang {
	case LangGo:
		method, ok := goRouteVerbs[simple]
		if !ok || !hasDot || len(args) == 0 || !x.isString(args[0].Kind()) {
			return
		}
		r := RouteRow{Repo: x.repo, Path: x.path, Method: method, Pattern: x.prefixOf(n) + unquote(x.text(&args[0])), Framework: "go", Line: line(n)}
		if len(args) > 1 && !x.isString(args[1].Kind()) {
			r.Handler = x.handlerArg(&args[1])
		}
		if m := x.chainedMethods(n); m != "" {
			r.Method = m
			r.Framework = "gorilla"
		}
		// A registration names its handler; `r.URL.Query().Get("q")` does not.
		if r.Handler == "" {
			return
		}
		x.out.Routes = append(x.out.Routes, x.placed(r, n))
	case LangJavaScript, LangTypeScript, LangTSX:
		method, ok := jsRouteVerbs[simple]
		if !ok || !hasDot || len(args) == 0 {
			return
		}
		pattern := ""
		if x.isString(args[0].Kind()) {
			pattern = unquote(x.text(&args[0]))
		} else if p := x.chainedRoutePattern(n); p != "" {
			pattern = p
		}
		if pattern == "" {
			return
		}
		r := RouteRow{Repo: x.repo, Path: x.path, Method: method, Pattern: x.prefixOf(n) + pattern, Framework: "express", Line: line(n)}
		for i := len(args) - 1; i >= 0; i-- {
			if x.isString(args[i].Kind()) {
				continue
			}
			r.Handler = x.handlerArg(&args[i])
			if x.isFunc(args[i].Kind()) {
				r.HandlerFn = x.fnID("<anon>", &args[i])
				if nm := x.ownerName(&args[i]); nm != "" {
					r.HandlerFn = x.fnID(nm, &args[i])
				}
			}
			break
		}
		if r.Handler == "" {
			return
		}
		x.out.Routes = append(x.out.Routes, x.placed(r, n))
	case LangPython:
		if simple != "path" && simple != "re_path" && simple != "url" {
			return
		}
		if len(args) < 2 || !x.isString(args[0].Kind()) {
			return
		}
		x.out.Routes = append(x.out.Routes, RouteRow{Repo: x.repo, Path: x.path, Pattern: unquote(x.text(&args[0])), Handler: x.handlerArg(&args[1]), Framework: "django", Line: line(n)})
	case LangRust:
		if simple != "route" || len(args) < 2 || !x.isString(args[0].Kind()) {
			return
		}
		r := RouteRow{Repo: x.repo, Path: x.path, Pattern: unquote(x.text(&args[0])), Framework: "axum", Line: line(n)}
		inner := &args[1]
		for inner != nil && x.isCall(inner.Kind()) {
			c := x.calleeNode(inner)
			if c == nil {
				break
			}
			verb := simpleName(x.flat(c))
			m, ok := rsRouteVerbs[verb]
			if !ok {
				break
			}
			if r.Method == "" {
				r.Method = m
			}
			ia := x.argNodes(inner)
			if len(ia) == 0 {
				break
			}
			if x.isCall(ia[0].Kind()) {
				inner = &ia[0]
				continue
			}
			r.Handler = x.handlerArg(&ia[0])
			break
		}
		if r.Handler == "" {
			return
		}
		x.out.Routes = append(x.out.Routes, r)
	}
}

// routerOfCall is the router a registration is made on: its variable,
// the prefix it carries here, and the parameter it came in through.
func (x *extractor) routerOfCall(call *ts.Node) (name string, info prefixInfo) {
	callee := x.calleeNode(call)
	if callee == nil {
		return "", prefixInfo{}
	}
	ids := x.idents(callee)
	if len(ids) == 0 {
		return "", prefixInfo{}
	}
	name = ids[0]
	if p, ok := x.prefixes[name]; ok {
		return name, p
	}
	if x.isParam(name) {
		return name, prefixInfo{param: name}
	}
	return name, prefixInfo{}
}

// prefixOf is the path the router a call is made on carries.
func (x *extractor) prefixOf(call *ts.Node) string {
	_, info := x.routerOfCall(call)
	return info.prefix
}

// placed fills a route's router and function, for the build to finish
// its pattern when the router came in as a parameter.
func (x *extractor) placed(r RouteRow, call *ts.Node) RouteRow {
	name, info := x.routerOfCall(call)
	r.FnID = x.fnIDOrEmpty()
	r.Router = name
	r.routerParam = info.param
	return r
}

// handlerArg names the handler an argument passes: a function inline (marked
// as a handler), a name, or the last segment of a selector.
func (x *extractor) handlerArg(a *ts.Node) string {
	if x.isFunc(a.Kind()) {
		x.handlerAt[a.StartByte()] = true
		if nm := x.ownerName(a); nm != "" {
			return nm
		}
		return "<anon>"
	}
	t := clean(x.flat(a), 120)
	// Wrappers such as `http.HandlerFunc(h)` or `checkAuth(h)`: the inner name.
	if x.isCall(a.Kind()) {
		if ia := x.argNodes(a); len(ia) == 1 {
			return x.handlerArg(&ia[0])
		}
	}
	return t
}

// chainedMethods reads gorilla's `.Methods("GET", http.MethodPost)` chained
// on a registration.
func (x *extractor) chainedMethods(n *ts.Node) string {
	p := n.Parent()
	if p == nil || p.Kind() != "selector_expression" {
		return ""
	}
	pp := p.Parent()
	if pp == nil || pp.Kind() != "call_expression" {
		return ""
	}
	f := pp.ChildByFieldName("function")
	if f == nil || !strings.HasSuffix(x.flat(f), ".Methods") {
		return ""
	}
	var ms []string
	args := x.argNodes(pp)
	for i := range args {
		t := x.flat(&args[i])
		if x.isString(args[i].Kind()) {
			ms = append(ms, strings.ToUpper(unquote(t)))
		} else if strings.HasPrefix(t, "http.Method") {
			ms = append(ms, strings.ToUpper(strings.TrimPrefix(t, "http.Method")))
		}
	}
	return strings.Join(ms, ",")
}

// chainedRoutePattern reads express's `router.route('/x').get(h)`.
func (x *extractor) chainedRoutePattern(n *ts.Node) string {
	callee := x.calleeNode(n)
	if callee == nil || callee.Kind() != "member_expression" {
		return ""
	}
	obj := callee.ChildByFieldName("object")
	if obj == nil || !x.isCall(obj.Kind()) {
		return ""
	}
	f := x.calleeNode(obj)
	if f == nil || simpleName(x.flat(f)) != "route" {
		return ""
	}
	args := x.argNodes(obj)
	if len(args) == 0 || !x.isString(args[0].Kind()) {
		return ""
	}
	return unquote(x.text(&args[0]))
}
