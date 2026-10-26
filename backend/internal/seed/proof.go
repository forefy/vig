package seed

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"image"
	"image/color"
	"image/png"

	"github.com/forefy/vig/backend/internal/core"
	"github.com/forefy/vig/backend/internal/store"
)

// A seeded finding whose evidence ran a tool carries a proof capture, as a
// submitted one must: a small generated picture standing in for the tool's
// screen, stored by content and held with role proof, so the demo report
// shows nothing of it and the document still passes the door.
func attachProofs(ctx context.Context, s *store.Store, auditID string, doc *core.FindingsDocument) error {
	for i := range doc.Findings {
		f := &doc.Findings[i]
		claim := f.Evidence.Ran()
		if claim == "" || f.Attached() > 0 {
			continue
		}
		raw := proofPNG(f.Ordinal, f.Evidence.Tool)
		sum := sha256.Sum256(raw)
		caption := claim + ": " + f.Evidence.Artifact
		if len(caption) > 140 {
			caption = caption[:137] + "..."
		}
		v, err := s.SaveVisual(ctx, store.Visual{AuditID: auditID, Ordinal: f.Ordinal, Caption: caption, Mime: "image/png", SHA256: hex.EncodeToString(sum[:]), Bytes: raw})
		if err != nil {
			return err
		}
		f.Visuals = append(f.Visuals, core.Visual{ID: v.ID, Kind: core.VisualImage, Caption: caption, Mime: "image/png", Size: v.Size, Role: core.VisualProof})
	}
	return nil
}

// proofPNG draws a tool-screen stand-in: a dark pane with a header band and
// rows of blocks whose widths follow the ordinal and the tool's name, so
// two findings never share bytes.
func proofPNG(ordinal int, tool string) []byte {
	const w, h = 480, 270
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	bg := color.RGBA{0x12, 0x12, 0x14, 0xff}
	band := color.RGBA{0x1f, 0x1f, 0x23, 0xff}
	ink := color.RGBA{0x3f, 0x3f, 0x46, 0xff}
	hot := color.RGBA{0x86, 0xef, 0xac, 0xff}
	fill := func(x0, y0, x1, y1 int, c color.RGBA) {
		for y := y0; y < y1 && y < h; y++ {
			for x := x0; x < x1 && x < w; x++ {
				img.SetRGBA(x, y, c)
			}
		}
	}
	fill(0, 0, w, h, bg)
	fill(0, 0, w, 28, band)
	seed := ordinal*131 + len(tool)*17
	for i, r := range tool {
		seed += int(r) * (i + 1)
	}
	for row := 0; row < 10; row++ {
		y := 40 + row*22
		width := 80 + (seed*(row+3)*7919)%300
		c := ink
		if (seed+row)%4 == 0 {
			c = hot
		}
		fill(24, y, 24+width, y+12, c)
	}
	var out bytes.Buffer
	_ = png.Encode(&out, img)
	return out.Bytes()
}
