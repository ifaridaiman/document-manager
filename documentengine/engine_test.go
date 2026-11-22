package documentengine

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/png"
	"strings"
	"testing"
)

// fixturePDF builds a 2-page Letter PDF. Each page has a heading near the top
// and a black rectangle filling the bottom-right quadrant (in unrotated space).
// Page 2 has /Rotate 90.
func fixturePDF() []byte {
	content := "BT /F1 24 Tf 72 720 Td (Revenue increased by 18%) Tj ET\n0 0 0 rg 306 0 306 396 re f\n"
	objs := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R 4 0 R] /Count 2 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 5 0 R /Resources << /Font << /F1 6 0 R >> >> >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Rotate 90 /Contents 5 0 R /Resources << /Font << /F1 6 0 R >> >> >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%sendstream", len(content), content),
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
		"<< /Title (Annual Report) /Author (ACME) /CreationDate (D:20240115093000+08'00') >>",
	}
	return buildPDF(objs, 7)
}

// buildPDF writes objs as objects 1..n with a correct xref; info is the object
// number of the document info dictionary (0 for none).
func buildPDF(objs []string, info int) []byte {
	var b bytes.Buffer
	b.WriteString("%PDF-1.4\n")
	offsets := make([]int, len(objs))
	for i, o := range objs {
		offsets[i] = b.Len()
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", i+1, o)
	}
	xref := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", len(objs)+1)
	for _, off := range offsets {
		fmt.Fprintf(&b, "%010d 00000 n \n", off)
	}
	infoRef := ""
	if info > 0 {
		infoRef = fmt.Sprintf(" /Info %d 0 R", info)
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R%s >>\nstartxref\n%d\n%%%%EOF\n", len(objs)+1, infoRef, xref)
	return b.Bytes()
}

func openFixture(t *testing.T) *Document {
	t.Helper()
	e, err := New(Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.Close() })
	d, err := e.OpenBytes(context.Background(), "report.pdf", fixturePDF())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

// meanLuma decodes a PNG and returns its average brightness (0 black .. 255 white).
func meanLuma(t *testing.T, data []byte) float64 {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	b := img.Bounds()
	var sum float64
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, _ := img.At(x, y).RGBA()
			sum += float64(r+g+bl) / 3 / 257
		}
	}
	return sum / float64(b.Dx()*b.Dy())
}

func code(err error) ErrorCode {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

func TestRegionValidate(t *testing.T) {
	ok := []Region{{0, 0, 1, 1}, {0.12, 0.25, 0.76, 0.32}, {0.9, 0.9, 0.1, 0.1}}
	bad := []Region{{-0.1, 0, 0.5, 0.5}, {0, 0, 0, 0.5}, {0.6, 0, 0.5, 0.5}, {0, 0.6, 0.5, 0.5}, {0, 0, 1.01, 1}}
	for _, r := range ok {
		if err := r.Validate(); err != nil {
			t.Errorf("%+v should be valid: %v", r, err)
		}
	}
	for _, r := range bad {
		if code(r.Validate()) != ErrInvalidRegion {
			t.Errorf("%+v should be INVALID_REGION", r)
		}
	}
}

func TestParsePDFDate(t *testing.T) {
	got := parsePDFDate("D:20240115093000+08'00'")
	if got == nil || got.UTC().Format("2006-01-02T15:04") != "2024-01-15T01:30" {
		t.Fatalf("got %v", got)
	}
	if d := parsePDFDate("D:2024"); d == nil || d.Year() != 2024 {
		t.Fatalf("year-only: %v", d)
	}
	if parsePDFDate("garbage") != nil {
		t.Fatal("garbage should be nil")
	}
}

func TestRotate(t *testing.T) {
	src := image.NewGray(image.Rect(0, 0, 3, 2))
	src.Pix[0] = 255 // top-left pixel white
	r := rotate(src, 90).(*image.Gray)
	if r.Bounds().Dx() != 2 || r.Bounds().Dy() != 3 || r.GrayAt(1, 0).Y != 255 {
		t.Fatalf("90° rotation wrong: %v %v", r.Bounds(), r.Pix)
	}
}

func TestOpenErrors(t *testing.T) {
	e, err := New(Config{MaxFileSize: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	ctx := context.Background()
	if _, err := e.OpenBytes(ctx, "x.pdf", []byte("hello")); code(err) != ErrInvalidPDF {
		t.Errorf("no signature: %v", err)
	}
	if _, err := e.OpenBytes(ctx, "x.pdf", []byte("%PDF-1.4 garbage")); code(err) != ErrInvalidPDF {
		t.Errorf("broken pdf: %v", err)
	}
	if _, err := e.OpenBytes(ctx, "x.pdf", make([]byte, 2<<20)); code(err) != ErrFileTooLarge {
		t.Errorf("too large: %v", err)
	}
}

func TestInspectAndText(t *testing.T) {
	d := openFixture(t)
	ctx := context.Background()
	m, _ := d.Inspect()
	if m.PageCount != 2 || m.Title != "Annual Report" || m.Author != "ACME" || m.CreationDate == nil || m.Encrypted {
		t.Fatalf("metadata: %+v", m)
	}
	p1, err := d.GetPageInfo(ctx, 1)
	if err != nil || p1.Width != 612 || p1.Height != 792 || p1.Rotation != 0 || !p1.TextExists {
		t.Fatalf("page1: %+v %v", p1, err)
	}
	p2, _ := d.GetPageInfo(ctx, 2)
	if p2.Width != 792 || p2.Height != 612 || p2.Rotation != 90 {
		t.Fatalf("page2 should be landscape after rotation: %+v", p2)
	}
	text, err := d.ExtractText(ctx, 1)
	if err != nil || !strings.Contains(text, "Revenue increased by 18%") {
		t.Fatalf("text %q %v", text, err)
	}
	if _, err := d.ExtractText(ctx, 3); code(err) != ErrPageNotFound {
		t.Fatalf("page 3: %v", err)
	}
}

func TestRenderAndCapture(t *testing.T) {
	d := openFixture(t)
	ctx := context.Background()

	r, err := d.RenderPage(ctx, RenderRequest{PageNumber: 1})
	if err != nil || abs(r.Width-1275) > 1 || abs(r.Height-1650) > 1 || r.MIMEType != "image/png" { // 8.5x11in @150dpi
		t.Fatalf("render: %dx%d %s %v", r.Width, r.Height, r.MIMEType, err)
	}

	cases := []struct {
		page   int
		region Region
		dark   bool
	}{
		{1, Region{0.5, 0.5, 0.5, 0.5}, true},  // the black quadrant
		{1, Region{0, 0.5, 0.5, 0.5}, false},   // bottom-left, empty
		{2, Region{0, 0.5, 0.5, 0.5}, true},    // rotated 90° CW: quadrant moves to bottom-left
		{2, Region{0.5, 0.5, 0.5, 0.5}, false}, // ...and bottom-right is empty
	}
	for _, c := range cases {
		res, err := d.CaptureRegion(ctx, CaptureRequest{PageNumber: c.page, Region: c.region, DPI: 72})
		if err != nil {
			t.Fatal(err)
		}
		l := meanLuma(t, res.Image)
		if c.dark != (l < 20) || (!c.dark && l < 235) {
			t.Errorf("page %d %+v: luma %.1f, want dark=%v", c.page, c.region, l, c.dark)
		}
	}

	res, err := d.CaptureRegion(ctx, CaptureRequest{PageNumber: 1, Region: Region{0.12, 0.25, 0.76, 0.32}, DPI: 300})
	if err != nil {
		t.Fatal(err)
	}
	ref := res.Reference
	if abs(res.Width-1938) > 1 || abs(res.Height-1056) > 1 || ref.PageNumber != 1 || ref.ImageWidth != res.Width || ref.DocumentID != d.ID {
		t.Fatalf("capture: %dx%d %+v", res.Width, res.Height, ref)
	}

	top, _ := d.CaptureRegion(ctx, CaptureRequest{PageNumber: 1, Region: Region{0, 0, 1, 0.15}, DPI: 72})
	if !strings.Contains(top.Reference.Excerpt, "Revenue") {
		t.Errorf("excerpt %q should contain heading", top.Reference.Excerpt)
	}
	bottom, _ := d.CaptureRegion(ctx, CaptureRequest{PageNumber: 1, Region: Region{0, 0.5, 1, 0.5}, DPI: 72})
	if bottom.Reference.Excerpt != "" {
		t.Errorf("bottom excerpt should be empty, got %q", bottom.Reference.Excerpt)
	}

	small, _ := d.CaptureRegion(ctx, CaptureRequest{PageNumber: 1, Region: Region{0, 0, 1, 1}, DPI: 300,
		Options: ImageOptions{MaxWidth: 400, Grayscale: true, Rotate: 90}})
	// portrait page rotated 90° is landscape; MaxWidth applies to the final (rotated) width
	if small.Width > 400 || small.Width < 390 || small.Height >= small.Width {
		t.Errorf("maxWidth/rotate not applied: %dx%d", small.Width, small.Height)
	}

	if _, err := d.CaptureRegion(ctx, CaptureRequest{PageNumber: 1, Region: Region{0, 0, 1, 1}, DPI: 9999}); code(err) != ErrInvalidRequest {
		t.Errorf("dpi limit: %v", err)
	}
}

func abs(n int) int { return max(n, -n) }
