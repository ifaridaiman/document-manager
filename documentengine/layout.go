package documentengine

import (
	"context"
	"math"
	"strings"

	"github.com/klippa-app/go-pdfium/requests"
)

// TextLine is one run of text as PDFium groups it (usually a line), with its
// box in the same normalized coordinates CaptureRegion takes.
type TextLine struct {
	Index  int    `json:"index"` // 0-based, in PDFium's reading order; stable for the same PDF
	Text   string `json:"text"`
	Region Region `json:"region"`
}

// PageLayout lists a page's text lines. Lines is empty for pages without a
// text layer (scanned pages).
type PageLayout struct {
	PageNumber int        `json:"pageNumber"`
	Width      float64    `json:"width"`  // points, as displayed
	Height     float64    `json:"height"` // points, as displayed
	Rotation   int        `json:"rotation"`
	Lines      []TextLine `json:"lines"`
}

// Layout returns the page's text lines with their positions, so a caller can
// cite lines by index and capture their exact boxes.
func (d *Document) Layout(ctx context.Context, pageNumber int) (PageLayout, error) {
	var out PageLayout
	err := d.do(ctx, func() error {
		return d.withPage(pageNumber, func(p requests.Page) error {
			size, err := d.inst.GetPageSize(&requests.GetPageSize{Page: p})
			if err != nil {
				return newErr(ErrTextExtractionFailed, "cannot read page size", "pageNumber", pageNumber)
			}
			out = PageLayout{PageNumber: pageNumber, Width: size.Width, Height: size.Height, Lines: []TextLine{}}
			if rot, err := d.inst.FPDFPage_GetRotation(&requests.FPDFPage_GetRotation{Page: p}); err == nil {
				out.Rotation = int(rot.PageRotation) * 90
			}

			tp, err := d.inst.FPDFText_LoadPage(&requests.FPDFText_LoadPage{Page: p})
			if err != nil {
				return newErr(ErrTextExtractionFailed, "cannot load text layer", "pageNumber", pageNumber)
			}
			defer d.inst.FPDFText_ClosePage(&requests.FPDFText_ClosePage{TextPage: tp.TextPage})

			n, err := d.inst.FPDFText_CountRects(&requests.FPDFText_CountRects{TextPage: tp.TextPage, StartIndex: 0, Count: -1})
			if err != nil || n.Count <= 0 {
				return nil // no text layer
			}

			// PDF user space → display space. PageToDevice flips y, applies the page
			// rotation and any MediaBox offset; dividing by the device size normalizes.
			const scale = 100 // device units per point
			sx, sy := int(math.Round(size.Width*scale)), int(math.Round(size.Height*scale))
			toNorm := func(px, py float64) (float64, float64, bool) {
				res, err := d.inst.FPDF_PageToDevice(&requests.FPDF_PageToDevice{Page: p, SizeX: sx, SizeY: sy, PageX: px, PageY: py})
				if err != nil {
					return 0, 0, false
				}
				return float64(res.DeviceX) / float64(sx), float64(res.DeviceY) / float64(sy), true
			}

			for i := 0; i < n.Count; i++ {
				r, err := d.inst.FPDFText_GetRect(&requests.FPDFText_GetRect{TextPage: tp.TextPage, Index: i})
				if err != nil {
					continue
				}
				bt, err := d.inst.FPDFText_GetBoundedText(&requests.FPDFText_GetBoundedText{
					TextPage: tp.TextPage, Left: r.Left, Top: r.Top, Right: r.Right, Bottom: r.Bottom,
				})
				if err != nil {
					continue
				}
				text := strings.TrimSpace(bt.Text)
				if text == "" {
					continue
				}
				x1, y1, ok1 := toNorm(r.Left, r.Top)
				x2, y2, ok2 := toNorm(r.Right, r.Bottom)
				if !ok1 || !ok2 {
					continue
				}
				// Clamp to the page; a box entirely off-page ends up with w or h <= 0.
				x0, y0 := max(0, min(x1, x2)), max(0, min(y1, y2))
				w, h := min(1, max(x1, x2))-x0, min(1, max(y1, y2))-y0
				if w <= 0 || h <= 0 {
					continue
				}
				out.Lines = append(out.Lines, TextLine{
					Index:  len(out.Lines),
					Text:   text,
					Region: Region{X: x0, Y: y0, Width: w, Height: h},
				})
			}
			return nil
		})
	})
	return out, err
}
