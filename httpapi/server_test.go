package httpapi

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	de "github.com/ifaridaiman/document-manager/documentengine"
)

func TestStatusMapping(t *testing.T) {
	for code, want := range map[de.ErrorCode]int{
		de.ErrInvalidRequest:    400,
		de.ErrInvalidRegion:     400,
		de.ErrFileTooLarge:      413,
		de.ErrInvalidPDF:        422,
		de.ErrPageNotFound:      422,
		de.ErrResourceLimit:     422,
		de.ErrEngineBusy:        503,
		de.ErrProcessingTimeout: 504,
		de.ErrRenderFailed:      500,
		de.ErrInternal:          500,
	} {
		if got := status(code); got != want {
			t.Errorf("%s: got %d, want %d", code, got, want)
		}
	}
}

// With the only worker held, a request must get 503 + Retry-After, not 422.
func TestBusyReturns503(t *testing.T) {
	eng, err := de.New(de.Config{Workers: 1, Timeout: 300 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	held, err := eng.OpenBytes(context.Background(), "held.pdf", minimalPDF())
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()

	srv := httptest.NewServer((&Server{Engine: eng, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}).Handler())
	defer srv.Close()

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("file", "a.pdf")
	fw.Write(minimalPDF())
	mw.Close()
	res, err := http.Post(srv.URL+"/v1/inspect", mw.FormDataContentType(), &body)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusServiceUnavailable || res.Header.Get("Retry-After") == "" {
		t.Fatalf("got %d, Retry-After %q", res.StatusCode, res.Header.Get("Retry-After"))
	}
}

// minimalPDF is a valid one-page PDF with a correct xref.
func minimalPDF() []byte {
	objs := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] >>",
	}
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
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objs)+1, xref)
	return b.Bytes()
}
