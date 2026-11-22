package documentengine

import (
	"context"
	"strings"
	"sync"
	"testing"
)

// One Document shared by many goroutines must give correct results and, under
// `go test -race`, no data races (go-pdfium frees render buffers outside its lock).
func TestSharedDocumentConcurrent(t *testing.T) {
	d := openFixture(t)
	ctx := context.Background()

	var wg sync.WaitGroup
	errs := make(chan string, 64)
	for i := range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			page := 1 + i%2
			l, err := d.Layout(ctx, page)
			if err != nil || len(l.Lines) != 1 {
				errs <- "layout: " + errString(err)
				return
			}
			c, err := d.CaptureRegion(ctx, CaptureRequest{PageNumber: page, Region: l.Lines[0].Region, DPI: 150})
			if err != nil || !strings.Contains(c.Reference.Excerpt, "Revenue") {
				errs <- "capture: " + errString(err) + " excerpt " + c.Reference.Excerpt
			}
			if txt, err := d.ExtractText(ctx, page); err != nil || !strings.Contains(txt, "Revenue") {
				errs <- "text: " + errString(err)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Error(e)
	}
}

// Close while other goroutines are mid-operation must neither deadlock nor crash.
func TestCloseDuringOperations(t *testing.T) {
	d := openFixture(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = d.RenderPage(ctx, RenderRequest{PageNumber: 1, DPI: 72}) // may fail once closed
		}()
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	if _, err := d.ExtractText(ctx, 1); code(err) != ErrInternal {
		t.Fatalf("after Close: %v", err)
	}
}

func errString(err error) string {
	if err == nil {
		return "<nil>"
	}
	return err.Error()
}
