package documentengine

import (
	"context"
	"math"
	"strings"

	"github.com/klippa-app/go-pdfium/requests"
	"github.com/klippa-app/go-pdfium/responses"
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
				return newErr(ErrTextExtractionFailed, "cannot read page size", "pageNumber", pageNumber).wrap(err)
			}
			// Lines starts non-nil so a scanned page encodes as [] in JSON, not null.
			out = PageLayout{PageNumber: pageNumber, Width: size.Width, Height: size.Height, Lines: []TextLine{}}
			if rot, err := d.inst.FPDFPage_GetRotation(&requests.FPDFPage_GetRotation{Page: p}); err == nil {
				out.Rotation = int(rot.PageRotation) * 90
			}

			tp, err := d.inst.FPDFText_LoadPage(&requests.FPDFText_LoadPage{Page: p})
			if err != nil {
				return newErr(ErrTextExtractionFailed, "cannot load text layer", "pageNumber", pageNumber).wrap(err)
			}
			defer d.inst.FPDFText_ClosePage(&requests.FPDFText_ClosePage{TextPage: tp.TextPage})

			n, err := d.inst.FPDFText_CountRects(&requests.FPDFText_CountRects{TextPage: tp.TextPage, Count: -1})
			if err != nil {
				return nil // no usable text layer: empty Lines
			}

			// box maps a PDFium text rect (PDF user space: points, origin bottom-left)
			// to a normalized Region (0..1, origin top-left, page as displayed).
			// PageToDevice does the y-flip, page rotation and any MediaBox offset.
			const scale = 100 // device units per point, for sub-point precision
			sx, sy := int(math.Round(size.Width*scale)), int(math.Round(size.Height*scale))
			box := func(r *responses.FPDFText_GetRect) (Region, bool) {
				a, err1 := d.inst.FPDF_PageToDevice(&requests.FPDF_PageToDevice{Page: p, SizeX: sx, SizeY: sy, PageX: r.Left, PageY: r.Top})
				b, err2 := d.inst.FPDF_PageToDevice(&requests.FPDF_PageToDevice{Page: p, SizeX: sx, SizeY: sy, PageX: r.Right, PageY: r.Bottom})
				if err1 != nil || err2 != nil {
					return Region{}, false
				}
				// Rotation can swap which corner is top-left, hence min/max. Clamp to the
				// page; a box entirely off-page ends up with w or h <= 0 and is dropped.
				x0 := max(0, float64(min(a.DeviceX, b.DeviceX))/float64(sx))
				y0 := max(0, float64(min(a.DeviceY, b.DeviceY))/float64(sy))
				w := min(1, float64(max(a.DeviceX, b.DeviceX))/float64(sx)) - x0
				h := min(1, float64(max(a.DeviceY, b.DeviceY))/float64(sy)) - y0
				return Region{X: x0, Y: y0, Width: w, Height: h}, w > 0 && h > 0
			}

			for i := range n.Count { // a negative Count (PDFium's error value) runs zero times
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
				region, ok := box(r)
				if text == "" || !ok {
					continue // whitespace-only or off-page runs are not citable
				}
				out.Lines = append(out.Lines, TextLine{Index: len(out.Lines), Text: text, Region: region})
			}
			return nil
		})
	})
	return out, err
}
