package documentengine

import (
	"context"
	"strings"
	"testing"
)

func TestLayout(t *testing.T) {
	d := openFixture(t)
	ctx := context.Background()

	// Page 1: one heading at x=72pt, baseline y=720pt (from the bottom) on a 612x792 page.
	l1, err := d.Layout(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if l1.Width != 612 || l1.Height != 792 || l1.Rotation != 0 || len(l1.Lines) != 1 {
		t.Fatalf("page 1 layout: %+v", l1)
	}
	line := l1.Lines[0]
	r := line.Region
	if line.Index != 0 || line.Text != "Revenue increased by 18%" {
		t.Fatalf("line: %+v", line)
	}
	if r.X < 0.10 || r.X > 0.13 || r.Y < 0.05 || r.Y > 0.10 || r.Width < r.Height {
		t.Fatalf("page 1 box should sit top-left and be wide: %+v", r)
	}
	if err := r.Validate(); err != nil {
		t.Fatalf("box must be a valid capture region: %v", err)
	}

	// The box plugs straight into CaptureRegion and captures the same text.
	c, err := d.CaptureRegion(ctx, CaptureRequest{PageNumber: 1, Region: r, DPI: 72})
	if err != nil || !strings.Contains(c.Reference.Excerpt, "Revenue increased by 18%") {
		t.Fatalf("capture of the line box: excerpt %q, err %v", c.Reference.Excerpt, err)
	}

	// Page 2 is rotated 90° clockwise: the heading runs down the right edge.
	l2, err := d.Layout(ctx, 2)
	if err != nil || l2.Rotation != 90 || l2.Width != 792 || len(l2.Lines) != 1 {
		t.Fatalf("page 2 layout: %+v %v", l2, err)
	}
	r2 := l2.Lines[0].Region
	if r2.Height <= r2.Width || r2.X < 0.85 || r2.Y < 0.08 || r2.Y > 0.16 {
		t.Fatalf("page 2 box should be tall, at the right edge, near the top: %+v", r2)
	}
	c2, err := d.CaptureRegion(ctx, CaptureRequest{PageNumber: 2, Region: r2, DPI: 72})
	if err != nil || !strings.Contains(c2.Reference.Excerpt, "Revenue") {
		t.Fatalf("capture of the rotated line box: excerpt %q, err %v", c2.Reference.Excerpt, err)
	}

	if _, err := d.Layout(ctx, 3); code(err) != ErrPageNotFound {
		t.Fatalf("page 3: %v", err)
	}
}
