package documentengine

import (
	"context"
	"testing"
	"time"
)

// When every worker is taken, OpenBytes must report ENGINE_BUSY (retryable),
// not RESOURCE_LIMIT (the caller's input is too big).
func TestOpenWhenAllWorkersBusy(t *testing.T) {
	e, err := New(Config{Workers: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	held, err := e.OpenBytes(context.Background(), "a.pdf", fixturePDF())
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if _, err := e.OpenBytes(ctx, "b.pdf", fixturePDF()); code(err) != ErrEngineBusy {
		t.Fatalf("want ENGINE_BUSY, got %v", err)
	}
}
