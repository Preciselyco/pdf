package pdf

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// TestPageTreeFailsClosedOnLimit verifies a limit tripped while resolving a
// page-tree kid is raised, not taken for a malformed kid and skipped, which
// would leave a document with fewer pages than it lists.
func TestPageTreeFailsClosedOnLimit(t *testing.T) {
	data := buildPDF(
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R 4 0 R] /Count 2 >>",
		"<< /Type /Page /Parent 2 0 R >>",
		"<< /Type /Page /Parent 2 0 R /Junk ("+strings.Repeat("x", 4096)+") >>",
	)
	r := openPDF(t, data)
	r.cache.budget.Store(1 << 10)
	if err := failure(t, func() error { r.NumPage(); return nil }); !errors.Is(err, ErrLimit) {
		t.Errorf("NumPage: got %v, want a panic wrapping ErrLimit", err)
	}
	if err := failure(t, func() error { r.Page(1); return nil }); !errors.Is(err, ErrLimit) {
		t.Errorf("Page: got %v, want a panic wrapping ErrLimit", err)
	}
}

// TestPageTreeSkipsMalformedKid verifies a kid that is merely malformed is
// still skipped, losing only itself.
func TestPageTreeSkipsMalformedKid(t *testing.T) {
	r := openPDF(t, buildPDF(
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R 4 0 R 5 0 R] /Count 3 >>",
		"<< /Type /Page /Parent 2 0 R >>",
		"<< 5 5 >>",
		"<< /Type /Page /Parent 2 0 R >>",
	))
	if got := r.NumPage(); got != 2 {
		t.Errorf("NumPage = %d, want 2", got)
	}
}

// TestCmapLimitSurfaces verifies a ToUnicode cmap over the entry cap fails
// extraction with ErrLimit rather than reading as no cmap.
func TestCmapLimitSurfaces(t *testing.T) {
	var cm strings.Builder
	for i := 0; i < maxCmapEntries+1; i += 100 {
		cm.WriteString("100 beginbfchar\n")
		for j := range 100 {
			fmt.Fprintf(&cm, "<%04x> <0041>\n", i+j)
		}
		cm.WriteString("endbfchar\n")
	}
	r := openPDF(t, pagePDF("/Resources << /Font << /F1 5 0 R >> >>",
		streamObj("BT /F1 12 Tf (A) Tj ET"),
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /ToUnicode 6 0 R >>",
		streamObj(cm.String()),
	))
	if _, err := r.Page(1).GetPlainText(nil); !errors.Is(err, ErrLimit) {
		t.Errorf("GetPlainText: got %v, want ErrLimit", err)
	}
}
