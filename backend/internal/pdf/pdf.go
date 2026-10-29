// Package pdf renders a report as a PDF: the same facts the page shows, in
// the same order, set in the identity faces on paper. Everything is drawn
// in-process with fpdf and the fonts embedded in the og package; no
// browser, no external service. The light palette from tokens.css is the
// one used, since a PDF is printed or read on white.
package pdf

import (
	"bytes"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"codeberg.org/go-pdf/fpdf"

	"github.com/forefy/vig/backend/internal/core"
	"github.com/forefy/vig/backend/internal/og"
)

// Input is a report as the reader may see it: the document already cut
// for the viewer (exploit paths, proof attachments), with what the page
// header carries beside it. Visual returns the stored bytes of an
// attachment by id; a nil Visual or a miss leaves the attachment as a row.
type Input struct {
	Doc         core.FindingsDocument
	Adapter     string
	Revision    int
	CreatedAt   time.Time
	PublishedAt *time.Time
	Visual      func(id string) (mime string, data []byte, ok bool)
}

// Render draws the report. The document is laid out twice: once to learn
// the page count, once to print it in every footer.
func Render(in Input) ([]byte, error) {
	_, pages, err := build(in, 0)
	if err != nil {
		return nil, err
	}
	out, _, err := build(in, pages)
	return out, err
}

// ---------- palette (tokens.css, light) ----------

type rgb struct{ r, g, b int }

func hex(v uint32) rgb { return rgb{int(v >> 16 & 0xff), int(v >> 8 & 0xff), int(v & 0xff)} }

var (
	fg        = hex(0x0f0f12)
	muted     = hex(0x52525b)
	faint     = hex(0xa1a1aa)
	border    = hex(0xe4e4e7)
	secondary = hex(0xf4f4f5)
	okInk     = hex(0x16a34a)
	okSoft    = hex(0xdcfce7)
	warnInk   = hex(0xca8a04)
	warnSoft  = hex(0xfef9c3)
	badInk    = hex(0xdc2626)
	badSoft   = hex(0xfee2e2)
)

// A tone is a badge's ink on its soft ground.
type tone struct{ ink, soft rgb }

var (
	neutral = tone{muted, secondary}
	pending = tone{faint, secondary}
	okTone  = tone{okInk, okSoft}
	warn    = tone{warnInk, warnSoft}
	bad     = tone{badInk, badSoft}

	severityTone = map[core.Severity]tone{
		core.SeverityCritical: {hex(0xb91c1c), hex(0xfecaca)},
		core.SeverityHigh:     {hex(0xef4444), hex(0xfee2e2)},
		core.SeverityMedium:   {hex(0xd97706), hex(0xfef3c7)},
		core.SeverityLow:      {hex(0x0284c7), hex(0xe0f2fe)},
		core.SeverityInfo:     {hex(0x52525b), hex(0xf4f4f5)},
	}
	statusTone = map[core.FindingStatus]tone{
		core.FindingOpen: neutral, core.FindingFixed: okTone, core.FindingAcknowledged: neutral, core.FindingDisputed: warn, core.FindingArchived: pending,
	}
	exploitableTone = map[core.ExploitableBy]tone{
		"anyone": bad, "any_user": bad, "any_signer": bad,
		"internal": warn, "account_owner": warn, "api_key": warn, "internal_network": warn, "other_program": warn, "admin": warn, "upgrade_authority": warn,
		"none": neutral,
	}
	matchTone = map[core.MatchStatus]tone{"verified": okTone, "self_built": okTone, "mismatch": bad, "unverified": neutral}
	matchWord = map[core.MatchStatus]string{"verified": "verified", "self_built": "self-built", "mismatch": "mismatch", "unverified": "unverified"}
	knownTone = map[core.KnownStatus]tone{"fixed": okTone, "open": bad, "acknowledged": neutral}
)

var severityOrder = []core.Severity{core.SeverityCritical, core.SeverityHigh, core.SeverityMedium, core.SeverityLow, core.SeverityInfo}

// ---------- the chain's words (frontend/src/lib/chain.ts) ----------

type words struct {
	web                  bool
	program, instruction string
	height               string
	exploitable          map[core.ExploitableBy]string
}

func wordsFor(adapter string) words {
	evm, web, repo := adapter == "ethereum", adapter == "webapp", adapter == "repo"
	pick := func(w, e, r, s string) string {
		switch {
		case web:
			return w
		case evm:
			return e
		case repo:
			return r
		}
		return s
	}
	return words{
		web:         web,
		program:     pick("host", "contract", "package", "program"),
		instruction: pick("route", "function", "function", "instruction"),
		height:      pick("slot", "block", "slot", "slot"),
		exploitable: map[core.ExploitableBy]string{
			"anyone":            pick("anyone, no account", "anyone", "anyone", "anyone"),
			"any_user":          pick("any signed-in user", "any user", "any user", "any user"),
			"internal":          "insider (repo or telemetry)",
			"any_signer":        pick("any session", "any account", "any signer", "any signer"),
			"account_owner":     pick("account member", "position owner", "account owner", "account owner"),
			"api_key":           "API key holder",
			"internal_network":  "internal network",
			"admin":             pick("account admin", "admin", "admin", "admin"),
			"upgrade_authority": pick("platform operator", "upgrade admin", "upgrade authority", "upgrade authority"),
			"other_program":     pick("another service", "other contract", "other program", "other program"),
			"none":              "no one",
		},
	}
}

// ---------- geometry ----------

const (
	pageW, pageH = 210.0, 297.0
	margin       = 16.0
	bottom       = 18.0
	contentW     = pageW - 2*margin
	right        = pageW - margin
	limit        = pageH - bottom

	bodyPt, bodyLH   = 9.5, 4.7
	smallPt, smallLH = 8.0, 4.0
	codePt, codeLH   = 7.6, 3.8
	labelPt          = 6.4
	badgeH           = 4.3
	bulletIndent     = 4.0
	gap              = 1.6
)

type doc struct {
	*fpdf.Fpdf
	in      Input
	words   words
	total   int
	links   map[int]int    // finding ordinal -> link id
	byTitle map[string]int // linkKey(title) -> ordinal
	titles  map[int]string
}

// build lays the document out once and returns its bytes and page count.
func build(in Input, total int) ([]byte, int, error) {
	p := fpdf.New("P", "mm", "A4", "")
	for _, f := range []struct{ family, style, file string }{
		{"sans", "", "InstrumentSans.ttf"},
		{"mono", "", "GeistMono-Regular.ttf"},
		{"mono", "B", "GeistMono-Medium.ttf"},
	} {
		b, err := og.Font(f.file)
		if err != nil {
			return nil, 0, err
		}
		p.AddUTF8FontFromBytes(f.family, f.style, b)
	}
	p.SetMargins(margin, margin, margin)
	p.SetAutoPageBreak(true, bottom)
	p.SetTitle(in.Doc.Report.Pin.Repo+" audit report", true)
	p.SetCreator("", true)
	d := &doc{Fpdf: p, in: in, words: wordsFor(in.Adapter), total: total, links: map[int]int{}, byTitle: map[string]int{}, titles: map[int]string{}}
	for _, f := range in.Doc.Findings {
		d.links[f.Ordinal] = p.AddLink()
		d.byTitle[linkKey(f.Title)] = f.Ordinal
		d.titles[f.Ordinal] = f.Title
	}
	p.SetFooterFunc(d.footer)
	d.cover()
	d.findings()
	if err := p.Error(); err != nil {
		return nil, 0, err
	}
	pages := p.PageNo()
	var buf bytes.Buffer
	if err := p.Output(&buf); err != nil {
		return nil, 0, err
	}
	return buf.Bytes(), pages, nil
}

func (d *doc) ink(c rgb)  { d.SetTextColor(c.r, c.g, c.b) }
func (d *doc) fill(c rgb) { d.SetFillColor(c.r, c.g, c.b) }
func (d *doc) line(c rgb) { d.SetDrawColor(c.r, c.g, c.b) }

func (d *doc) sans(pt float64)  { d.SetFont("sans", "", pt) }
func (d *doc) mono(pt float64)  { d.SetFont("mono", "", pt) }
func (d *doc) monoB(pt float64) { d.SetFont("mono", "B", pt) }

// need starts a new page when h more millimetres would run past the break.
func (d *doc) need(h float64) {
	if d.GetY()+h > limit {
		d.AddPage()
	}
}

func (d *doc) footer() {
	d.SetY(-12)
	d.mono(6.4)
	d.ink(faint)
	pin := d.in.Doc.Report.Pin
	left := pin.Repo
	if pin.Commit != "" {
		left += "  " + short(pin.Commit)
	}
	if d.in.Revision > 1 {
		left += fmt.Sprintf("  rev %d", d.in.Revision)
	}
	d.CellFormat(contentW/2, 4, left, "", 0, "L", false, 0, "")
	page := fmt.Sprintf("%d", d.PageNo())
	if d.total > 0 {
		page = fmt.Sprintf("%d / %d", d.PageNo(), d.total)
	}
	d.CellFormat(contentW/2, 4, page, "", 0, "R", false, 0, "")
}

// ---------- small pieces ----------

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

func date(t time.Time) string { return t.UTC().Format("2006-01-02") }
func when(t time.Time) string { return t.UTC().Format("2006-01-02 15:04 UTC") }

func linkKey(title string) string { return strings.ToLower(strings.Join(strings.Fields(title), " ")) }

// label is the caps label the page sets over a group of facts.
func (d *doc) label(s string, c rgb) {
	d.mono(labelPt)
	d.ink(c)
	d.CellFormat(0, 3.6, strings.ToUpper(s), "", 1, "L", false, 0, "")
}

// labelAt draws a caps label inline and advances x, for a label in front of
// a chip.
func (d *doc) labelAt(s string) float64 {
	d.mono(labelPt)
	d.ink(faint)
	w := d.GetStringWidth(strings.ToUpper(s)) + 1.4
	d.CellFormat(w, badgeH, strings.ToUpper(s), "", 0, "L", false, 0, "")
	return w
}

// badge draws a chip at the cursor and advances x past it.
func (d *doc) badge(text string, t tone) float64 {
	d.mono(6.8)
	x, y := d.GetX(), d.GetY()
	pad := 1.5
	w := d.GetStringWidth(text) + 2*pad
	d.fill(t.soft)
	d.RoundedRect(x, y, w, badgeH, 0.9, "1234", "F")
	d.ink(t.ink)
	d.SetXY(x+pad, y)
	d.CellFormat(w-2*pad, badgeH, text, "", 0, "L", false, 0, "")
	d.SetXY(x+w, y)
	return w
}

// chip is one item of a facts row: an optional caps kind in front of a
// badge, or plain mono text.
type chip struct {
	kind  string
	text  string
	tone  tone
	plain bool
}

// chips lays chips in a row that wraps at the right margin.
func (d *doc) chips(items []chip, indent float64) {
	x0 := margin + indent
	d.SetX(x0)
	for _, c := range items {
		d.mono(labelPt)
		w := 0.0
		if c.kind != "" {
			w += d.GetStringWidth(strings.ToUpper(c.kind)) + 1.4
		}
		d.mono(6.8)
		if c.plain {
			w += d.GetStringWidth(c.text)
		} else {
			w += d.GetStringWidth(c.text) + 3
		}
		if d.GetX() > x0 && d.GetX()+w > right {
			d.Ln(badgeH + 1.2)
			d.SetX(x0)
		}
		if c.kind != "" {
			d.labelAt(c.kind)
		}
		if c.plain {
			d.mono(6.8)
			d.ink(muted)
			d.CellFormat(d.GetStringWidth(c.text), badgeH, c.text, "", 0, "L", false, 0, "")
		} else {
			d.badge(c.text, c.tone)
		}
		d.SetX(d.GetX() + 2.4)
	}
	d.Ln(badgeH + 1.2)
}

// ---------- prose ----------

var (
	codeRE = regexp.MustCompile("`[^`]+`")
	linkRE = regexp.MustCompile(`\[\[([^\[\]\n]+)\]\]`)
	pathRE = regexp.MustCompile(`((?:[\w.-]+/)*[\w.-]+\.(?:sol|rs|ts|tsx|js|go|toml|json|md)(?::\d+(?:-\d+)?)?)`)
)

// inline writes one run of prose at the cursor, flowing to the margins:
// backtick spans and paths in mono, [[Title]] as a link to that finding.
func (d *doc) inline(text string, pt, lh float64) {
	write := func(s string) {
		if s == "" {
			return
		}
		d.sans(pt)
		d.ink(fg)
		d.Write(lh, s)
	}
	code := func(s string) {
		d.mono(pt - 0.9)
		d.ink(hex(0x3f3f46))
		d.Write(lh, s)
	}
	plain := func(s string) {
		for _, piece := range splitRE(pathRE, s) {
			if piece.match {
				code(piece.text)
			} else {
				write(piece.text)
			}
		}
	}
	for _, seg := range splitRE(codeRE, text) {
		if seg.match {
			code(seg.text[1 : len(seg.text)-1])
			continue
		}
		for _, piece := range splitRE(linkRE, seg.text) {
			if !piece.match {
				plain(piece.text)
				continue
			}
			title := strings.TrimSpace(piece.text[2 : len(piece.text)-2])
			n, ok := d.byTitle[linkKey(title)]
			if !ok {
				write(title)
				continue
			}
			d.SetFont("sans", "U", pt)
			d.ink(fg)
			d.WriteLinkID(lh, fmt.Sprintf("%02d %s", n, d.titles[n]), d.links[n])
		}
	}
}

type piece struct {
	text  string
	match bool
}

func splitRE(re *regexp.Regexp, s string) []piece {
	var out []piece
	last := 0
	for _, m := range re.FindAllStringIndex(s, -1) {
		if m[0] > last {
			out = append(out, piece{text: s[last:m[0]]})
		}
		out = append(out, piece{text: s[m[0]:m[1]], match: true})
		last = m[1]
	}
	if last < len(s) {
		out = append(out, piece{text: s[last:]})
	}
	return out
}

type proseOpt struct {
	indent float64
	fix    bool // a fence right after a bullet nests under it, shown as a patch when placed
	pt, lh float64
	sites  []core.FixSite
}

// prose sets finding prose: paragraphs, bullets and fences, in order.
func (d *doc) prose(text string, o proseOpt) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	if o.pt == 0 {
		o.pt, o.lh = bodyPt, bodyLH
	}
	blocks, err := core.ProseBlocks(text)
	if err != nil {
		blocks = []core.ProseBlock{{Text: text}}
	}
	afterBullet := false
	for _, b := range blocks {
		if b.Code {
			indent := o.indent
			if o.fix && afterBullet {
				indent += bulletIndent
			}
			d.fence(b, indent, o.sites)
			continue
		}
		afterBullet = false
		var para []string
		flush := func() {
			if len(para) > 0 {
				d.para(strings.Join(para, " "), o)
				para = nil
			}
		}
		for _, l := range strings.Split(b.Text, "\n") {
			l = strings.TrimSpace(l)
			if strings.HasPrefix(l, "- ") {
				flush()
				d.bullet(strings.TrimPrefix(l, "- "), o)
				afterBullet = true
				continue
			}
			if l != "" {
				para = append(para, l)
			}
		}
		flush()
	}
}

func (d *doc) para(text string, o proseOpt) {
	d.need(o.lh * 2)
	d.SetLeftMargin(margin + o.indent)
	d.SetX(margin + o.indent)
	d.inline(text, o.pt, o.lh)
	d.Ln(o.lh)
	d.SetLeftMargin(margin)
	d.Ln(1.4)
}

func (d *doc) bullet(text string, o proseOpt) {
	d.need(o.lh * 2)
	x0 := margin + o.indent
	d.SetX(x0)
	d.mono(o.pt)
	d.ink(faint)
	d.CellFormat(bulletIndent, o.lh, "-", "", 0, "L", false, 0, "")
	d.SetLeftMargin(x0 + bulletIndent)
	d.SetX(x0 + bulletIndent)
	d.inline(text, o.pt, o.lh)
	d.Ln(o.lh)
	d.SetLeftMargin(margin)
	d.Ln(0.6)
}

// codeLines prints lines of code on a ground, each wrapped to the width,
// with an optional gutter of line numbers and a mark column.
type codeLine struct {
	num  int    // 0 for none
	mark string // "", "-", "+"
	text string
	hot  bool // the located lines
}

func (d *doc) code(lines []codeLine, indent float64, numbered bool) {
	x0 := margin + indent
	w := right - x0
	gutter := 0.0
	if numbered {
		gutter = 7.5
	}
	markW := 0.0
	for _, l := range lines {
		if l.mark != "" {
			markW = 4
			break
		}
	}
	pad := 2.0
	textW := w - gutter - markW - 2*pad
	d.fill(secondary)
	d.SetX(x0)
	d.CellFormat(w, 1.4, "", "", 1, "L", true, 0, "")
	for _, l := range lines {
		ground := secondary
		switch {
		case l.mark == "-":
			ground = badSoft
		case l.mark == "+":
			ground = okSoft
		case l.hot:
			ground = warnSoft
		}
		d.mono(codePt)
		text := strings.ReplaceAll(l.text, "\t", "    ")
		wrapped := d.SplitText(text, textW)
		if len(wrapped) == 0 {
			wrapped = []string{""}
		}
		for i, part := range wrapped {
			d.need(codeLH)
			d.SetX(x0)
			d.fill(ground)
			if numbered {
				d.ink(faint)
				n := ""
				if i == 0 && l.num > 0 {
					n = fmt.Sprintf("%d", l.num)
				}
				d.CellFormat(gutter, codeLH, n, "", 0, "R", true, 0, "")
			}
			if markW > 0 {
				m := ""
				if i == 0 {
					m = l.mark
				}
				d.monoB(codePt)
				switch l.mark {
				case "-":
					d.ink(badInk)
				case "+":
					d.ink(okInk)
				default:
					d.ink(muted)
				}
				d.CellFormat(markW, codeLH, m, "", 0, "C", true, 0, "")
				d.mono(codePt)
			}
			d.ink(fg)
			d.CellFormat(w-gutter-markW, codeLH, "  "+part, "", 1, "L", true, 0, "")
		}
	}
	d.fill(secondary)
	d.SetX(x0)
	d.CellFormat(w, 1.4, "", "", 1, "L", true, 0, "")
	d.Ln(1.6)
}

// fence sets a ``` block. One whose info line names a place, with the
// server's capture of those lines beside it, reads as a patch: the lines
// it replaces, then the lines it puts there.
func (d *doc) fence(b core.ProseBlock, indent float64, sites []core.FixSite) {
	d.need(codeLH * 3)
	header := b.Info
	var old *core.Snippet
	loc, located := core.FenceLocation(b.Info)
	if located {
		header = loc.File + ":" + fmt.Sprint(loc.LineStart)
		if loc.LineEnd > 0 {
			header += "-" + fmt.Sprint(loc.LineEnd)
		}
		for i := range sites {
			s := sites[i]
			if s.Location.File == loc.File && s.Location.LineStart == loc.LineStart && s.Snippet != nil {
				old = s.Snippet
			}
		}
	}
	if header != "" {
		d.mono(6.6)
		d.ink(muted)
		d.SetX(margin + indent)
		d.CellFormat(0, 3.6, header, "", 1, "L", false, 0, "")
	}
	var lines []codeLine
	if old != nil {
		// The capture is the enclosing item; only the located lines go, and
		// the fence's lines take their place.
		end := loc.LineEnd
		if end < loc.LineStart {
			end = loc.LineStart
		}
		placed := false
		for i, l := range strings.Split(old.Code, "\n") {
			n := old.StartLine + i
			if n > end && !placed {
				for _, nl := range strings.Split(b.Text, "\n") {
					lines = append(lines, codeLine{mark: "+", text: nl})
				}
				placed = true
			}
			mark := ""
			if n >= loc.LineStart && n <= end {
				mark = "-"
			}
			lines = append(lines, codeLine{num: n, mark: mark, text: l})
		}
		if !placed {
			for _, nl := range strings.Split(b.Text, "\n") {
				lines = append(lines, codeLine{mark: "+", text: nl})
			}
		}
		d.code(lines, indent, true)
		return
	}
	for _, l := range strings.Split(b.Text, "\n") {
		lines = append(lines, codeLine{text: l})
	}
	d.code(lines, indent, false)
}

// place is where a location points, as the page prints it: a web location
// is its endpoint, a source one its file and lines.
func place(loc core.Location) string {
	url := loc.URL
	if url == "" && (strings.HasPrefix(strings.ToLower(loc.File), "http://") || strings.HasPrefix(strings.ToLower(loc.File), "https://")) {
		url = loc.File
	}
	if url != "" {
		return url
	}
	s := loc.File
	if loc.LineStart > 0 {
		s += fmt.Sprintf(":%d", loc.LineStart)
		if loc.LineEnd > loc.LineStart {
			s += fmt.Sprintf("-%d", loc.LineEnd)
		}
	}
	return s
}

// snippet sets the code around a location with the located lines marked.
func (d *doc) snippet(loc core.Location, s *core.Snippet, note string, indent float64) {
	d.need(codeLH * 4)
	d.monoB(7.2)
	d.ink(fg)
	d.SetX(margin + indent)
	d.CellFormat(0, 4, place(loc), "", 1, "L", false, 0, "")
	if note != "" {
		d.sans(smallPt)
		d.ink(muted)
		d.SetX(margin + indent)
		d.MultiCell(contentW-indent, smallLH, note, "", "L", false)
	}
	if s == nil || s.Code == "" {
		d.Ln(1)
		return
	}
	var lines []codeLine
	for i, l := range strings.Split(s.Code, "\n") {
		n := s.StartLine + i
		hot := n >= loc.LineStart && (n <= loc.LineEnd || (loc.LineEnd == 0 && n == loc.LineStart))
		lines = append(lines, codeLine{num: n, text: l, hot: hot})
	}
	d.code(lines, indent, true)
}

// visual embeds a picture, or sets a row for a recording or a file.
func (d *doc) visual(v core.Visual, indent float64) {
	kind := v.Kind
	if kind == "" {
		kind = core.VisualKindOf(v.Mime)
	}
	if kind == core.VisualImage && d.in.Visual != nil {
		if mime, data, ok := d.in.Visual(v.ID); ok {
			if t := imageType(mime); t != "" {
				name := "v-" + v.ID
				info := d.RegisterImageOptionsReader(name, fpdf.ImageOptions{ImageType: t}, bytes.NewReader(data))
				if d.Ok() && info != nil && info.Width() > 0 {
					// Natural size at 110 dpi, capped at the column.
					w := info.Width() * 72 / 110
					if w > contentW-indent {
						w = contentW - indent
					}
					h := w * info.Height() / info.Width()
					if h > limit-margin-20 {
						h = limit - margin - 20
						w = h * info.Width() / info.Height()
					}
					d.need(h + 8)
					d.ImageOptions(name, margin+indent, d.GetY(), w, h, true, fpdf.ImageOptions{ImageType: t}, 0, "")
					d.Ln(1.2)
					d.caption(v, indent)
					return
				}
				d.ClearError()
			}
		}
	}
	d.need(badgeH * 2)
	d.SetX(margin + indent)
	d.badge(string(kind), neutral)
	d.SetX(d.GetX() + 2)
	d.mono(6.8)
	d.ink(muted)
	meta := v.Name
	if v.Size > 0 {
		meta = strings.TrimSpace(meta + " " + size(v.Size))
	}
	if meta == "" {
		meta = v.Mime
	}
	d.CellFormat(0, badgeH, meta, "", 1, "L", false, 0, "")
	d.caption(v, indent)
}

func (d *doc) caption(v core.Visual, indent float64) {
	d.sans(smallPt)
	d.ink(muted)
	d.SetX(margin + indent)
	d.MultiCell(contentW-indent, smallLH, v.Caption, "", "L", false)
	d.Ln(1.6)
}

func imageType(mime string) string {
	switch strings.ToLower(mime) {
	case "image/png":
		return "PNG"
	case "image/jpeg", "image/jpg":
		return "JPEG"
	case "image/gif":
		return "GIF"
	}
	return ""
}

func size(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

// ---------- the cover ----------

func (d *doc) cover() {
	d.AddPage()
	rep := d.in.Doc.Report
	pin := rep.Pin
	eyebrow := "audit report  " + date(d.in.CreatedAt)
	if d.in.PublishedAt != nil {
		eyebrow = "audit report  published " + date(*d.in.PublishedAt)
	}
	if d.in.Revision > 1 {
		eyebrow += fmt.Sprintf("  rev %d", d.in.Revision)
	}
	d.label(eyebrow, muted)
	d.Ln(2)
	d.mono(19)
	d.ink(fg)
	d.MultiCell(contentW, 9, pin.Repo, "", "L", false)
	d.Ln(3)

	// Scope: exactly what was reviewed, every pin in full.
	d.label("scope", faint)
	d.Ln(0.6)
	repoWord := "repo"
	if d.words.web {
		repoWord = "site"
	}
	d.fact(repoWord, pin.Repo, fg)
	if pin.Commit != "" {
		c := pin.Commit
		if pin.BaseSHA != "" {
			c = pin.BaseSHA + ".." + c
		}
		d.fact("commit", c, muted)
	}
	for _, p := range pin.Programs {
		x := d.fact(d.words.program, p.ID, fg)
		d.SetXY(x+1.5, d.GetY()-smallLH)
		d.badge(matchWord[p.Match], matchTone[p.Match])
		if p.Slot > 0 {
			d.SetX(d.GetX() + 2)
			d.mono(6.8)
			d.ink(faint)
			d.CellFormat(0, badgeH, fmt.Sprintf("%s %d", d.words.height, p.Slot), "", 0, "L", false, 0, "")
		}
		d.Ln(smallLH)
	}
	if len(pin.Excluded) > 0 {
		d.fact("excluded", strings.Join(pin.Excluded, "  "), muted)
	}
	d.Ln(3)

	// The five counts.
	s := rep.Summary
	counts := []struct {
		sev core.Severity
		n   int
	}{{core.SeverityCritical, s.Critical}, {core.SeverityHigh, s.High}, {core.SeverityMedium, s.Medium}, {core.SeverityLow, s.Low}, {core.SeverityInfo, s.Info}}
	cw := contentW / 5
	y := d.GetY()
	d.line(border)
	d.SetLineWidth(0.2)
	for i, c := range counts {
		x := margin + float64(i)*cw
		d.Rect(x, y, cw, 16, "D")
		d.SetXY(x+3, y+2.5)
		d.monoB(15)
		if c.n == 0 {
			d.ink(faint)
		} else {
			d.ink(severityTone[c.sev].ink)
		}
		d.CellFormat(cw-6, 7, fmt.Sprint(c.n), "", 0, "L", false, 0, "")
		d.SetXY(x+3, y+10.2)
		d.mono(labelPt)
		d.ink(muted)
		d.CellFormat(cw-6, 3.6, strings.ToUpper(string(c.sev)), "", 0, "L", false, 0, "")
	}
	d.SetXY(margin, y+16)
	d.Ln(4)

	// Methods: what ran.
	if m := rep.Methods; m != nil {
		var items []chip
		for _, a := range []struct {
			on   bool
			word string
		}{{m.Static, "static"}, {m.Bytecode, "bytecode"}, {m.OnChain, "on-chain"}, {m.Live, "live"}} {
			if a.on {
				items = append(items, chip{text: a.word, tone: neutral})
			}
		}
		if len(items) > 0 {
			items[0].kind = "agents"
		}
		if fz := m.Fuzzing; fz != nil {
			t := neutral
			if fz.Crashes > 0 {
				t = warn
			}
			items = append(items, chip{kind: "tools", text: fz.Tool, tone: t}, chip{text: campaign(*fz), plain: true})
		}
		if len(items) > 0 {
			d.label("methods", faint)
			d.Ln(0.6)
			d.chips(items, 0)
			d.Ln(2)
		}
	}

	// Known: what earlier reports raised on this code.
	if len(rep.Known) > 0 {
		d.label("known", faint)
		d.Ln(0.6)
		for i, k := range rep.Known {
			d.need(badgeH + 2)
			d.mono(6.8)
			d.ink(faint)
			d.CellFormat(7, badgeH, fmt.Sprintf("%02d", i+1), "", 0, "L", false, 0, "")
			t := severityTone[k.Severity]
			d.fill(t.ink)
			d.Circle(d.GetX()+1.2, d.GetY()+badgeH/2, 1.0, "F")
			d.SetX(d.GetX() + 4)
			d.sans(smallPt)
			d.ink(fg)
			w := contentW - 11 - 48
			d.CellFormat(w, badgeH, trunc(d, k.Title, w), "", 0, "L", false, 0, "")
			d.badge(k.Source, pending)
			d.SetX(d.GetX() + 2)
			word := string(k.Status)
			if k.Status == "fixed" {
				word = "fixed at commit"
			}
			d.badge(word, knownTone[k.Status])
			d.Ln(badgeH + 1.2)
		}
		d.Ln(2)
	}

	// Contents: every finding, in the order the report reads.
	findings, archived := d.ordered()
	d.label("findings", faint)
	d.Ln(0.6)
	if len(findings) == 0 && len(archived) == 0 {
		d.badge("0 findings", okTone)
		d.Ln(badgeH + 2)
	}
	for _, f := range findings {
		d.contentsRow(f)
	}
	if len(archived) > 0 {
		d.Ln(1.5)
		d.label("archived", faint)
		d.Ln(0.6)
		for _, f := range archived {
			d.contentsRow(f)
		}
	}
}

func (d *doc) contentsRow(f core.Finding) {
	d.need(badgeH + 2)
	y := d.GetY()
	d.mono(6.8)
	d.ink(faint)
	d.CellFormat(7, badgeH, fmt.Sprintf("%02d", f.Ordinal), "", 0, "L", false, 0, "")
	sw := d.badge(string(f.Severity), severityTone[f.Severity])
	d.SetX(d.GetX() + 2)
	d.mono(6.8)
	stw := d.GetStringWidth(string(f.Status)) + 3
	w := right - d.GetX() - stw - 2
	d.sans(smallPt)
	d.ink(fg)
	d.CellFormat(w, badgeH, trunc(d, f.Title, w), "", 0, "L", false, d.links[f.Ordinal], "")
	d.SetXY(right-stw, y)
	d.badge(string(f.Status), statusTone[f.Status])
	_ = sw
	d.Ln(badgeH + 1.2)
}

// fact is one row of the scope: a caps kind, then a mono value.
func (d *doc) fact(kind, value string, c rgb) float64 {
	d.need(smallLH)
	d.mono(labelPt)
	d.ink(faint)
	d.CellFormat(18, smallLH, strings.ToUpper(kind), "", 0, "L", false, 0, "")
	d.mono(7.4)
	d.ink(c)
	w := d.GetStringWidth(value)
	if w > contentW-18 {
		d.MultiCell(contentW-18, smallLH, value, "", "L", false)
		return right
	}
	d.CellFormat(w, smallLH, value, "", 1, "L", false, 0, "")
	return margin + 18 + w
}

func trunc(d *doc, s string, w float64) string {
	if d.GetStringWidth(s) <= w {
		return s
	}
	r := []rune(s)
	for len(r) > 0 && d.GetStringWidth(string(r)+"…") > w {
		r = r[:len(r)-1]
	}
	return string(r) + "…"
}

func campaign(fz core.Fuzzing) string {
	parts := []string{fmt.Sprintf("%d runs", fz.Runs)}
	if fz.Harnesses == 1 {
		parts = append(parts, "1 harness")
	} else if fz.Harnesses > 1 {
		parts = append(parts, fmt.Sprintf("%d harnesses", fz.Harnesses))
	}
	if fz.Crashes == 1 {
		parts = append(parts, "1 crash")
	} else {
		parts = append(parts, fmt.Sprintf("%d crashes", fz.Crashes))
	}
	if h := fz.DurationS / 3600; h > 0 {
		parts = append(parts, fmt.Sprintf("%dh", h))
	}
	if fz.Coverage != "" {
		parts = append(parts, fz.Coverage)
	}
	return strings.Join(parts, "  ")
}

// ordered lists the findings the way the page does: by severity, then by
// ordinal, with the archived ones apart.
func (d *doc) ordered() (findings, archived []core.Finding) {
	rank := map[core.Severity]int{}
	for i, s := range severityOrder {
		rank[s] = i
	}
	for _, f := range d.in.Doc.Findings {
		if f.Status == core.FindingArchived {
			archived = append(archived, f)
		} else {
			findings = append(findings, f)
		}
	}
	sort.SliceStable(findings, func(i, j int) bool {
		if rank[findings[i].Severity] != rank[findings[j].Severity] {
			return rank[findings[i].Severity] < rank[findings[j].Severity]
		}
		return findings[i].Ordinal < findings[j].Ordinal
	})
	return findings, archived
}

// ---------- the findings ----------

func (d *doc) findings() {
	findings, archived := d.ordered()
	if len(findings)+len(archived) == 0 {
		return
	}
	d.AddPage()
	var last core.Severity
	for _, f := range findings {
		if f.Severity != last {
			d.need(40)
			if last != "" {
				d.Ln(4)
			}
			d.mono(7.4)
			d.SetX(margin)
			d.badge(string(f.Severity), severityTone[f.Severity])
			d.Ln(badgeH + 3)
			last = f.Severity
		}
		d.finding(f)
	}
	if len(archived) > 0 {
		d.need(40)
		d.Ln(4)
		d.SetX(margin)
		d.badge("archived", pending)
		d.Ln(badgeH + 3)
		for _, f := range archived {
			d.finding(f)
		}
	}
}

func (d *doc) finding(f core.Finding) {
	d.need(42)
	d.SetLink(d.links[f.Ordinal], -1, -1)
	d.line(border)
	d.SetLineWidth(0.2)
	y := d.GetY()
	d.Line(margin, y, right, y)
	d.Ln(3.5)

	// Header: ordinal and title, the rating and the status at the corner.
	d.mono(6.8)
	sw := d.GetStringWidth(string(f.Severity)) + 3
	stw := d.GetStringWidth(string(f.Status)) + 3
	badges := sw + 2 + stw
	y = d.GetY()
	d.mono(8.5)
	d.ink(muted)
	d.CellFormat(9, 6, fmt.Sprintf("%02d", f.Ordinal), "", 0, "L", false, 0, "")
	d.sans(12.5)
	d.ink(fg)
	d.MultiCell(contentW-9-badges-4, 6, f.Title, "", "L", false)
	after := d.GetY()
	d.SetXY(right-badges, y+0.9)
	d.badge(string(f.Severity), severityTone[f.Severity])
	d.SetX(d.GetX() + 2)
	d.badge(string(f.Status), statusTone[f.Status])
	d.SetXY(margin, after)
	d.Ln(1.5)

	// Facts: the scope the finding sits in, who reaches it, what proved it.
	var items []chip
	loc := f.Location
	if u := webURL(loc); u != "" {
		items = append(items, chip{kind: "host", text: hostOf(u), tone: neutral})
	}
	if loc.Instruction != "" {
		items = append(items, chip{kind: d.words.instruction, text: loc.Instruction, tone: neutral})
	}
	if loc.Package != "" {
		items = append(items, chip{kind: "package", text: loc.Package, tone: neutral})
	}
	if loc.ProgramID != "" {
		items = append(items, chip{kind: d.words.program, text: loc.ProgramID, tone: neutral})
	}
	if f.ExploitableBy != "" {
		w := d.words.exploitable[f.ExploitableBy]
		if w == "" {
			w = strings.ReplaceAll(string(f.ExploitableBy), "_", " ")
		}
		t, ok := exploitableTone[f.ExploitableBy]
		if !ok {
			t = neutral
		}
		items = append(items, chip{kind: "exploitable by", text: w, tone: t})
	}
	if e := f.Evidence; e != nil {
		word := e.Tool
		kind := "tools"
		if word == "" {
			word = string(e.Kind)
			kind = "evidence"
		}
		t := neutral
		if e.Kind == "fuzz" || e.Kind == "onchain" {
			t = pending
		}
		items = append(items, chip{kind: kind, text: word, tone: t})
		if e.Artifact != "" {
			items = append(items, chip{text: e.Artifact, plain: true})
		}
	}
	if f.CarriedFromID != "" {
		items = append(items, chip{text: "carried", tone: neutral})
	}
	if f.ExploitPathWithheld {
		items = append(items, chip{text: "exploit path withheld", tone: warn})
	}
	if len(items) > 0 {
		d.chips(items, 0)
		d.Ln(0.5)
	}

	// What the reproduction ruled out: one dot per item, filled when done.
	if len(f.Exhaustion) > 0 {
		done := 0
		for _, a := range f.Exhaustion {
			if a.Done {
				done++
			}
		}
		d.need(smallLH * float64(len(f.Exhaustion)-done+2))
		d.mono(labelPt)
		d.ink(faint)
		d.CellFormat(d.GetStringWidth("RULED OUT")+2, badgeH, "RULED OUT", "", 0, "L", false, 0, "")
		cy := d.GetY() + badgeH/2
		x := d.GetX() + 1
		for _, a := range f.Exhaustion {
			if a.Done {
				d.fill(okInk)
				d.Circle(x, cy, 0.9, "F")
			} else {
				d.line(faint)
				d.SetLineWidth(0.25)
				d.Circle(x, cy, 0.9, "D")
			}
			x += 3
		}
		d.SetX(x + 1)
		d.mono(6.8)
		d.ink(muted)
		d.CellFormat(0, badgeH, fmt.Sprintf("%d / %d", done, len(f.Exhaustion)), "", 1, "L", false, 0, "")
		// Only what was not tried to the end is spelled out, with why.
		for _, a := range f.Exhaustion {
			if a.Done {
				continue
			}
			d.need(smallLH)
			d.line(faint)
			d.SetLineWidth(0.25)
			d.Circle(margin+1.5, d.GetY()+smallLH/2, 0.9, "D")
			text := a.Text
			if text == "" {
				text = a.Key
			}
			d.SetLeftMargin(margin + 4.5)
			d.SetX(margin + 4.5)
			d.sans(smallPt)
			d.ink(muted)
			d.Write(smallLH, text)
			if a.Note != "" {
				d.mono(6.6)
				d.ink(faint)
				d.Write(smallLH, "   "+a.Note)
			}
			d.SetLeftMargin(margin)
			d.Ln(smallLH)
		}
		d.Ln(1.5)
	}

	// Every correction, newest first.
	d.history(f)

	d.prose(f.Description, proseOpt{})
	d.Ln(1)

	// The chain: how the code is used, what it promises, where it breaks,
	// what that costs.
	if f.NormalUse != "" {
		d.section("normal use", muted)
		d.prose(f.NormalUse, proseOpt{})
	}
	if f.Expected != "" {
		d.section("expected", muted)
		d.prose(f.Expected, proseOpt{})
	}
	steps := []core.Site{{Location: f.Location, Snippet: f.Snippet}}
	steps = append(steps, f.Sites...)
	hasCode := f.Snippet != nil || len(f.Sites) > 0 || webURL(f.Location) != ""
	if hasCode {
		word := "code"
		if f.SitesKind != "" && len(f.Sites) > 0 {
			word = string(f.SitesKind)
		}
		d.section(word, muted)
		for i, s := range steps {
			if len(steps) > 1 {
				d.mono(labelPt)
				d.ink(faint)
				d.SetX(margin)
				d.CellFormat(0, 3.4, fmt.Sprintf("STEP %02d", i+1), "", 1, "L", false, 0, "")
			}
			d.snippet(s.Location, s.Snippet, s.Note, 0)
			for _, v := range s.Visuals {
				d.visual(v, 0)
			}
		}
	}
	if f.Impact != "" || len(f.Visuals) > 0 {
		d.section("impact", muted)
		d.prose(f.Impact, proseOpt{})
		for _, v := range f.Visuals {
			d.visual(v, 0)
		}
	}
	if f.ExploitPath != "" {
		d.section("exploit path", badInk)
		d.prose(f.ExploitPath, proseOpt{})
	}
	if f.Fix != "" {
		d.section("fix", okInk)
		d.prose(f.Fix, proseOpt{fix: true, sites: f.FixSites})
	}
	if len(f.References) > 0 {
		d.section("references", muted)
		codeFor := map[string]core.ReferenceCode{}
		for _, rc := range f.ReferenceCode {
			codeFor[rc.URL] = rc
		}
		for _, ref := range f.References {
			d.need(smallLH)
			d.mono(7)
			d.ink(muted)
			d.SetX(margin)
			d.WriteLinkString(smallLH, ref, ref)
			d.Ln(smallLH + 0.4)
			if rc, ok := codeFor[ref]; ok && rc.Code.Code != "" {
				var lines []codeLine
				for i, l := range strings.Split(rc.Code.Code, "\n") {
					lines = append(lines, codeLine{num: rc.Code.StartLine + i, text: l})
				}
				d.mono(6.6)
				d.ink(faint)
				d.SetX(margin)
				d.CellFormat(0, 3.4, fmt.Sprintf("%s %s %s:%d-%d", rc.Repo, short(rc.Commit), rc.File, rc.Start, rc.End), "", 1, "L", false, 0, "")
				d.code(lines, 0, true)
			}
		}
	}
	d.Ln(4)
}

func (d *doc) section(name string, c rgb) {
	d.need(bodyLH * 3)
	d.Ln(0.6)
	d.label(name, c)
	d.Ln(0.8)
}

// history sets every re-rating and status move on the finding, newest
// first: what it was, what it became, why, who, when.
func (d *doc) history(f core.Finding) {
	type change struct {
		from, to   string
		fromT, toT tone
		reason, by string
		at         time.Time
	}
	var rows []change
	for _, c := range f.SeverityChanges {
		rows = append(rows, change{string(c.From), string(c.To), severityTone[c.From], severityTone[c.To], c.Reason, c.By, c.At})
	}
	for _, c := range f.StatusChanges {
		rows = append(rows, change{string(c.From), string(c.To), statusTone[c.From], statusTone[c.To], c.Reason, c.By, c.At})
	}
	if len(rows) == 0 {
		return
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].at.After(rows[j].at) })
	x0 := margin + 2
	for _, c := range rows {
		d.need(badgeH + smallLH + 2)
		y := d.GetY()
		d.line(border)
		d.SetLineWidth(0.3)
		d.Line(margin, y, margin, y+badgeH)
		d.SetXY(x0, y)
		d.badge(c.from, c.fromT)
		d.SetX(d.GetX() + 1.5)
		d.mono(6.8)
		d.ink(faint)
		d.CellFormat(d.GetStringWidth("->")+1, badgeH, "->", "", 0, "L", false, 0, "")
		d.SetX(d.GetX() + 1)
		d.badge(c.to, c.toT)
		d.SetX(d.GetX() + 3)
		d.mono(6.6)
		d.ink(faint)
		d.CellFormat(0, badgeH, c.by+"  "+when(c.at), "", 1, "L", false, 0, "")
		if c.reason != "" {
			d.SetX(x0)
			d.sans(smallPt)
			d.ink(muted)
			d.MultiCell(contentW-2, smallLH, c.reason, "", "L", false)
		}
		d.Ln(1.2)
	}
	d.Ln(1)
}

func webURL(loc core.Location) string {
	if loc.URL != "" {
		return loc.URL
	}
	if l := strings.ToLower(loc.File); strings.HasPrefix(l, "http://") || strings.HasPrefix(l, "https://") {
		return loc.File
	}
	return ""
}

func hostOf(u string) string {
	s := u
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	if i := strings.IndexAny(s, "/?#"); i >= 0 {
		s = s[:i]
	}
	return strings.ToLower(s)
}
