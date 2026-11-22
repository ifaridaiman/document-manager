package documentengine

import (
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
)

// The cause stays reachable for Go callers and logs, but never reaches JSON.
func TestErrorCause(t *testing.T) {
	err := error(newErr(ErrRenderFailed, "render failed", "pageNumber", 3).wrap(io.ErrUnexpectedEOF))

	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatal("errors.Is must see the cause")
	}
	var e *Error
	if !errors.As(err, &e) || e.Code != ErrRenderFailed {
		t.Fatalf("errors.As: %v", err)
	}
	if !strings.Contains(err.Error(), "unexpected EOF") {
		t.Fatalf("Error() should include the cause for logs: %q", err.Error())
	}
	b, _ := json.Marshal(e)
	if strings.Contains(string(b), "EOF") || strings.Contains(string(b), "cause") {
		t.Fatalf("cause leaked into JSON: %s", b)
	}
	if string(b) != `{"code":"RENDER_FAILED","message":"render failed","details":{"pageNumber":3}}` {
		t.Fatalf("unexpected JSON: %s", b)
	}
}

// A broken PDF's PDFium error is kept as the cause, not exposed in Details.
func TestOpenErrorKeepsCause(t *testing.T) {
	e, err := New(Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	_, err = e.OpenBytes(t.Context(), "x.pdf", []byte("%PDF-1.4 garbage"))
	var ee *Error
	if !errors.As(err, &ee) || ee.Code != ErrInvalidPDF {
		t.Fatalf("got %v", err)
	}
	if errors.Unwrap(err) == nil {
		t.Fatal("PDFium cause should be attached")
	}
	if _, leaked := ee.Details["cause"]; leaked {
		t.Fatal("cause must not be in Details")
	}
}
