package documentengine

import (
	"context"
	"testing"
	"time"
)

func TestParsePDFDateMalformedZones(t *testing.T) {
	for _, in := range []string{
		"D:2024+5'", // crashed the parser: zone shorter than two digits
		"D:2024+",
		"D:2024+''",
		"D:20240115093000-'",
		"D:20240115093000+ab'cd'",
		"D:20240115093000+99'00'",
		"D:20240115093000+08'xx'",
	} {
		got := parsePDFDate(in)
		if got == nil || got.Year() != 2024 {
			t.Errorf("%q: want the 2024 date kept, got %v", in, got)
		}
	}
	if got := parsePDFDate("D:20240115093000-05'30'"); got == nil || got.UTC().Format(time.RFC3339) != "2024-01-15T15:00:00Z" {
		t.Errorf("valid negative zone: %v", got)
	}
}

// A PDF whose metadata date is malformed must still open.
func TestOpenWithMalformedDate(t *testing.T) {
	e, err := New(Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	pdf := buildPDF([]string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] >>",
		"<< /Title (Odd date) /CreationDate (D:2024+5') >>",
	}, 4)
	d, err := e.OpenBytes(context.Background(), "odd.pdf", pdf)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer d.Close()
	if d.Metadata.Title != "Odd date" || d.Metadata.CreationDate == nil || d.Metadata.CreationDate.Year() != 2024 {
		t.Fatalf("metadata: %+v", d.Metadata)
	}
}

// Metadata comes from untrusted files: the parser must never panic.
func FuzzParsePDFDate(f *testing.F) {
	for _, s := range []string{"D:20240115093000+08'00'", "D:2024", "D:2024+5'", "D:2024-", "", "D:", "garbage", "D:20240115093000Z"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		parsePDFDate(s)
	})
}
