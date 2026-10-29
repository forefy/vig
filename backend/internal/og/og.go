// Package og renders the 1200x630 preview images that link unfurlers fetch
// for public reports: the mark, the repository, the pin line and the five
// severity counts on the dark ground. Everything is drawn in-process with
// image/draw and the embedded identity fonts; no browser, no external
// service.
package og

import (
	"bytes"
	"embed"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"sync"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

//go:embed fonts/*.ttf
var fontFS embed.FS

// Width and Height are the OpenGraph recommended dimensions.
const (
	Width  = 1200
	Height = 630
)

// Identity palette (docs/identity.md, dark).
var (
	Ground     = rgb(0x0f0f12)
	Card       = rgb(0x18181b)
	Border     = rgb(0x27272a)
	Foreground = rgb(0xfafafa)
	Muted      = rgb(0xa1a1aa)
	Accent     = rgb(0x86efac)
	OK         = rgb(0x82e28d)
	Warn       = rgb(0xfacc15)
	Bad        = rgb(0xf87171)
	// The severity ladder, the dark theme's values from tokens.css: critical
	// the deepest red, high a lighter red, medium amber, low blue, info grey.
	SevCritical = rgb(0xef4444)
	SevHigh     = rgb(0xfca5a5)
	SevMedium   = rgb(0xfbbf24)
	SevLow      = rgb(0x7dd3fc)
	SevInfo     = rgb(0xa1a1aa)
	Pending     = rgb(0x71717a)
)

func rgb(v uint32) color.NRGBA {
	return color.NRGBA{R: uint8(v >> 16), G: uint8(v >> 8), B: uint8(v), A: 0xff}
}

func alpha(c color.NRGBA, a float64) color.NRGBA {
	c.A = uint8(math.Round(255 * a))
	return c
}

// Report is what a public report's image shows.
type Report struct {
	Repo      string // owner/name
	Commit    string // full sha; the image shows the first 7
	Programs  int
	Adapter   string // solana (programs) or ethereum (contracts)
	Match     string // verified, self_built, mismatch, unverified
	Critical  int
	High      int
	Medium    int
	Low       int
	Info      int
	SlugLabel string // optional footer, e.g. the public URL host
}

// ---------- fonts ----------

type faces struct {
	mono, monoMedium, sans, serif *opentype.Font
}

var (
	loadOnce sync.Once
	loaded   faces
	loadErr  error
)

func load() (faces, error) {
	loadOnce.Do(func() {
		parse := func(name string) *opentype.Font {
			if loadErr != nil {
				return nil
			}
			b, err := fontFS.ReadFile("fonts/" + name)
			if err != nil {
				loadErr = err
				return nil
			}
			f, err := opentype.Parse(b)
			if err != nil {
				loadErr = fmt.Errorf("%s: %w", name, err)
				return nil
			}
			return f
		}
		loaded = faces{
			mono:       parse("GeistMono-Regular.ttf"),
			monoMedium: parse("GeistMono-Medium.ttf"),
			sans:       parse("InstrumentSans.ttf"),
			serif:      parse("InstrumentSerif-Italic.ttf"),
		}
	})
	return loaded, loadErr
}

func face(f *opentype.Font, size float64) (font.Face, error) {
	return opentype.NewFace(f, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingNone})
}

// ---------- canvas ----------

type canvas struct {
	img *image.NRGBA
}

func newCanvas() *canvas {
	img := image.NewNRGBA(image.Rect(0, 0, Width, Height))
	draw.Draw(img, img.Bounds(), image.NewUniform(Ground), image.Point{}, draw.Src)
	return &canvas{img: img}
}

// blend composites c over the pixel at x,y with coverage cov in [0,1].
func (cv *canvas) blend(x, y int, c color.NRGBA, cov float64) {
	if x < 0 || y < 0 || x >= Width || y >= Height || cov <= 0 {
		return
	}
	a := float64(c.A) / 255 * cov
	if a <= 0 {
		return
	}
	i := cv.img.PixOffset(x, y)
	p := cv.img.Pix[i : i+4 : i+4]
	p[0] = uint8(float64(c.R)*a + float64(p[0])*(1-a) + 0.5)
	p[1] = uint8(float64(c.G)*a + float64(p[1])*(1-a) + 0.5)
	p[2] = uint8(float64(c.B)*a + float64(p[2])*(1-a) + 0.5)
	p[3] = 0xff
}

// coverage is a one-pixel-wide linear ramp around a signed distance.
func coverage(d float64) float64 {
	switch {
	case d <= -0.5:
		return 1
	case d >= 0.5:
		return 0
	}
	return 0.5 - d
}

// disc fills an anti-aliased circle.
func (cv *canvas) disc(cx, cy, r float64, c color.NRGBA) {
	for y := int(cy - r - 1); y <= int(cy+r+1); y++ {
		for x := int(cx - r - 1); x <= int(cx+r+1); x++ {
			d := math.Hypot(float64(x)+0.5-cx, float64(y)+0.5-cy) - r
			cv.blend(x, y, c, coverage(d))
		}
	}
}

// ring strokes an arc of a circle with round caps. gapFrom and gapTo are
// angles in degrees, measured clockwise on screen from 3 o'clock, that are
// left open.
func (cv *canvas) ring(cx, cy, r, stroke, gapFrom, gapTo float64, c color.NRGBA) {
	half := stroke / 2
	inGap := func(deg float64) bool {
		for deg < 0 {
			deg += 360
		}
		deg = math.Mod(deg, 360)
		return deg > gapFrom && deg < gapTo
	}
	outer := r + half + 1
	for y := int(cy - outer); y <= int(cy+outer); y++ {
		for x := int(cx - outer); x <= int(cx+outer); x++ {
			dx, dy := float64(x)+0.5-cx, float64(y)+0.5-cy
			deg := math.Atan2(dy, dx) * 180 / math.Pi
			if inGap(deg) {
				continue
			}
			d := math.Abs(math.Hypot(dx, dy)-r) - half
			cv.blend(x, y, c, coverage(d))
		}
	}
	for _, deg := range []float64{gapFrom, gapTo} {
		rad := deg * math.Pi / 180
		cv.disc(cx+r*math.Cos(rad), cy+r*math.Sin(rad), half, c)
	}
}

// roundedRect fills a rectangle with radius rad, optionally stroked.
func (cv *canvas) roundedRect(x0, y0, x1, y1, rad float64, fill color.NRGBA, stroke float64, edge color.NRGBA) {
	sdf := func(px, py float64) float64 {
		cx := math.Max(x0+rad, math.Min(px, x1-rad))
		cy := math.Max(y0+rad, math.Min(py, y1-rad))
		return math.Hypot(px-cx, py-cy) - rad
	}
	for y := int(y0 - 1); y <= int(y1+1); y++ {
		for x := int(x0 - 1); x <= int(x1+1); x++ {
			d := sdf(float64(x)+0.5, float64(y)+0.5)
			cv.blend(x, y, fill, coverage(d))
			if stroke > 0 {
				cv.blend(x, y, edge, coverage(math.Abs(d+stroke/2)-stroke/2))
			}
		}
	}
}

// segment strokes a line from a to b with round caps.
func (cv *canvas) segment(ax, ay, bx, by, stroke float64, c color.NRGBA) {
	half := stroke / 2
	x0, x1 := math.Min(ax, bx)-half-1, math.Max(ax, bx)+half+1
	y0, y1 := math.Min(ay, by)-half-1, math.Max(ay, by)+half+1
	vx, vy := bx-ax, by-ay
	ll := vx*vx + vy*vy
	for y := int(y0); y <= int(y1); y++ {
		for x := int(x0); x <= int(x1); x++ {
			px, py := float64(x)+0.5, float64(y)+0.5
			t := ((px-ax)*vx + (py-ay)*vy) / ll
			t = math.Max(0, math.Min(1, t))
			d := math.Hypot(px-(ax+t*vx), py-(ay+t*vy)) - half
			cv.blend(x, y, c, coverage(d))
		}
	}
}

// mark draws the identity mark with its top-left corner at x,y and the given
// size, matching frontend/public/mark.svg: two strokes (6,7)-(16,27)-(26,7)
// at 4.2/32 with round caps, and a point at (16,12) of radius 2.4/32 in the
// accent.
func (cv *canvas) mark(x, y, size float64) {
	s := size / 32
	cv.segment(x+6*s, y+7*s, x+16*s, y+27*s, 4.2*s, Foreground)
	cv.segment(x+16*s, y+27*s, x+26*s, y+7*s, 4.2*s, Foreground)
	cv.disc(x+16*s, y+12*s, 2.4*s, Accent)
}

// text draws s with its baseline at y and returns the advance in pixels.
func (cv *canvas) text(f font.Face, x, y float64, s string, c color.NRGBA) float64 {
	d := &font.Drawer{Dst: cv.img, Src: image.NewUniform(c), Face: f, Dot: fixed.P(int(x), int(y))}
	d.DrawString(s)
	return float64(d.Dot.X-fixed.I(int(x))) / 64
}

func measure(f font.Face, s string) float64 {
	return float64(font.MeasureString(f, s)) / 64
}

// fit truncates s with an ellipsis so it measures at most max pixels.
func fit(f font.Face, s string, max float64) string {
	if measure(f, s) <= max {
		return s
	}
	r := []rune(s)
	for len(r) > 1 {
		r = r[:len(r)-1]
		if measure(f, string(r)+"…") <= max {
			return string(r) + "…"
		}
	}
	return "…"
}

// ---------- images ----------

const margin = 64

// Default renders the site-wide image used by every page without a report.
func Default() ([]byte, error) {
	fs, err := load()
	if err != nil {
		return nil, err
	}
	cv := newCanvas()
	cv.mark(margin, 56, 56)

	title, err := face(fs.sans, 84)
	if err != nil {
		return nil, err
	}
	cv.text(title, margin, 322, "Find security bugs other agents miss.", Foreground)
	sub, err := face(fs.sans, 32)
	if err != nil {
		return nil, err
	}
	cv.text(sub, margin, 382, "Agentic security audits, down to the bytecode.", Muted)

	// A quiet accent rule and a mono footer keep it on the same grid as the
	// report image.
	cv.roundedRect(margin, 470, margin+96, 474, 2, Accent, 0, Accent)
	mono, err := face(fs.mono, 22)
	if err != nil {
		return nil, err
	}
	cv.tracked(mono, margin, 528, "COMMIT  ·  SLOT  ·  BYTECODE SHA-256", 2.2, Pending)
	return encode(cv)
}

// Render draws one report's preview.
func Render(r Report) ([]byte, error) {
	fs, err := load()
	if err != nil {
		return nil, err
	}
	cv := newCanvas()
	cv.mark(margin, 56, 56)

	// Repository, large.
	repoFace, err := face(fs.monoMedium, 64)
	if err != nil {
		return nil, err
	}
	repo := r.Repo
	if repo == "" {
		repo = "unknown/repository"
	}
	cv.text(repoFace, margin, 226, fit(repoFace, repo, Width-2*margin), Foreground)

	// Pin line: short sha · n programs · match.
	pinFace, err := face(fs.mono, 28)
	if err != nil {
		return nil, err
	}
	sha := r.Commit
	if len(sha) > 7 {
		sha = sha[:7]
	}
	if sha == "" {
		sha = "-------"
	}
	x := float64(margin)
	y := 290.0
	x += cv.text(pinFace, x, y, sha, Foreground)
	x += cv.text(pinFace, x, y, "  ·  ", Pending)
	one, many := "program", "programs"
	if r.Adapter == "ethereum" {
		one, many = "contract", "contracts"
	}
	x += cv.text(pinFace, x, y, fmt.Sprintf("%d %s", r.Programs, plural(r.Programs, one, many)), Muted)
	x += cv.text(pinFace, x, y, "  ·  ", Pending)
	word, col := matchWord(r.Match)
	cv.text(pinFace, x, y, word, col)

	// Severity tiles along the bottom.
	labelFace, err := face(fs.mono, 18)
	if err != nil {
		return nil, err
	}
	countFace, err := face(fs.monoMedium, 60)
	if err != nil {
		return nil, err
	}
	tiles := []struct {
		label string
		n     int
		c     color.NRGBA
	}{
		{"CRITICAL", r.Critical, SevCritical},
		{"HIGH", r.High, SevHigh},
		{"MEDIUM", r.Medium, SevMedium},
		{"LOW", r.Low, SevLow},
		{"INFO", r.Info, SevInfo},
	}
	const gap = 16.0
	w := (float64(Width) - 2*margin - gap*4) / 5
	top, bottom := 398.0, 566.0
	for i, t := range tiles {
		x0 := float64(margin) + float64(i)*(w+gap)
		cv.roundedRect(x0, top, x0+w, bottom, 8, Card, 1, Border)
		// A colour edge along the top of the tile; the count in the same colour.
		cv.roundedRect(x0+1, top+1, x0+w-1, top+5, 2, t.c, 0, t.c)
		cv.tracked(labelFace, x0+24, top+52, t.label, 1.8, Muted)
		cv.text(countFace, x0+22, bottom-32, fmt.Sprint(t.n), t.c)
	}
	return encode(cv)
}

func encode(cv *canvas) ([]byte, error) {
	var buf bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.BestSpeed}
	if err := enc.Encode(&buf, cv.img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// tracked draws s one glyph at a time with extra advance, the label-caps
// tracking of the UI (0.1em).
func (cv *canvas) tracked(f font.Face, x, y float64, s string, tracking float64, c color.NRGBA) {
	for _, r := range s {
		x += cv.text(f, x, y, string(r), c) + tracking
	}
}

func matchWord(m string) (string, color.NRGBA) {
	switch m {
	case "verified":
		return "verified", OK
	case "self_built":
		return "self-built", OK
	case "mismatch":
		return "mismatch", Bad
	default:
		return "unverified", Pending
	}
}

// WorstMatch folds per-program match states into the one word the pin line
// shows: mismatch beats unverified beats self_built beats verified.
func WorstMatch(states []string) string {
	if len(states) == 0 {
		return "unverified"
	}
	rank := map[string]int{"verified": 0, "self_built": 1, "unverified": 2, "mismatch": 3}
	worst := "verified"
	for _, s := range states {
		if rank[s] > rank[worst] {
			worst = s
		}
	}
	return worst
}

// Font returns one of the embedded identity faces by file name, for a
// renderer that needs the bytes rather than a parsed face.
func Font(name string) ([]byte, error) {
	return fontFS.ReadFile("fonts/" + name)
}
