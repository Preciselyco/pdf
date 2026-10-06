package pdf

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// flatPages returns a file whose page tree lists n pages in one /Kids array.
func flatPages(n int) []byte {
	objs := []string{"<< /Type /Catalog /Pages 2 0 R >>", ""}
	var kids strings.Builder
	for i := range n {
		fmt.Fprintf(&kids, "%d 0 R ", 3+i)
		objs = append(objs, "<< /Type /Page /Parent 2 0 R >>")
	}
	objs[1] = fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", kids.String(), n)
	return buildPDF(objs...)
}

// TestPageCountBoundary verifies a file may list exactly 2,000 pages, and
// that one more is refused with ErrLimit.
func TestPageCountBoundary(t *testing.T) {
	if got := openPDF(t, flatPages(2000)).NumPage(); got != 2000 {
		t.Errorf("2000 pages: NumPage = %d, want 2000", got)
	}
	r := openPDF(t, flatPages(2001))
	if err := failure(t, func() error { r.NumPage(); return nil }); !errors.Is(err, ErrLimit) {
		t.Errorf("2001 pages: NumPage: got %v, want a panic wrapping ErrLimit", err)
	}
	// The refusal holds for every later call, not only the first.
	if err := failure(t, func() error { r.Page(1); return nil }); !errors.Is(err, ErrLimit) {
		t.Errorf("2001 pages: Page: got %v, want a panic wrapping ErrLimit", err)
	}
	if _, err := r.GetPlainText(); !errors.Is(err, ErrLimit) {
		t.Errorf("2001 pages: GetPlainText: got %v, want ErrLimit", err)
	}
}

// TestPageCountNested verifies the page cap counts pages under nested nodes,
// not only those of one /Kids array.
func TestPageCountNested(t *testing.T) {
	build := func(n int) []byte {
		objs := []string{"<< /Type /Catalog /Pages 2 0 R >>", "<< /Type /Pages /Kids [3 0 R 4 0 R] /Count 0 >>", "", ""}
		for half, obj := range []int{2, 3} {
			var kids strings.Builder
			count := n/2 + (n%2)*(1-half)
			for i := range count {
				fmt.Fprintf(&kids, "%d 0 R ", len(objs)+1+i)
			}
			objs[obj] = fmt.Sprintf("<< /Type /Pages /Parent 2 0 R /Kids [%s] /Count %d >>", kids.String(), count)
			for range count {
				objs = append(objs, fmt.Sprintf("<< /Type /Page /Parent %d 0 R >>", obj+1))
			}
		}
		return buildPDF(objs...)
	}
	if got := openPDF(t, build(2000)).NumPage(); got != 2000 {
		t.Errorf("2000 nested pages: NumPage = %d, want 2000", got)
	}
	r := openPDF(t, build(2001))
	if err := failure(t, func() error { r.NumPage(); return nil }); !errors.Is(err, ErrLimit) {
		t.Errorf("2001 nested pages: NumPage: got %v, want a panic wrapping ErrLimit", err)
	}
}

// TestDecodeBudgetFor verifies the decode budget of a file: the larger of
// 128 MiB and 32 times its size, capped at 512 MiB.
func TestDecodeBudgetFor(t *testing.T) {
	const MiB = 1 << 20
	tests := []struct {
		size int64
		want int64
	}{
		{0, 128 * MiB},
		{4*MiB - 1, 128 * MiB},
		{4 * MiB, 128 * MiB},
		{4*MiB + 1, 32 * (4*MiB + 1)},
		{10 * MiB, 320 * MiB},
		{16*MiB - 1, 32 * (16*MiB - 1)},
		{16 * MiB, 512 * MiB},
		{16*MiB + 1, 512 * MiB},
		{1 << 40, 512 * MiB},
	}
	for _, tt := range tests {
		if got := decodeBudgetFor(tt.size); got != tt.want {
			t.Errorf("decodeBudgetFor(%d) = %d, want %d", tt.size, got, tt.want)
		}
	}
}

// TestDecodeBudgetCap verifies a Reader's budget comes from decodeBudgetFor,
// that it may spend exactly that, and that one byte more is refused with
// ErrLimit.
func TestDecodeBudgetCap(t *testing.T) {
	data := pagePDF("", "null")
	r := openPDF(t, data)
	if want := decodeBudgetFor(int64(len(data))); r.cache.limit != want || want != 128<<20 {
		t.Fatalf("budget of a %d-byte file = %d, want %d", len(data), r.cache.limit, want)
	}
	if err := r.cache.spend(r.cache.limit); err != nil {
		t.Errorf("spending the whole budget: %v", err)
	}
	if err := r.cache.spend(1); !errors.Is(err, ErrLimit) {
		t.Errorf("spending one byte past the budget: got %v, want ErrLimit", err)
	}
}

// TestGlyphBudgetBoundary verifies a Reader's pages may show exactly
// 6,000,000 glyphs between them, 262,144 at most each, and that one more is
// refused with ErrLimit.
func TestGlyphBudgetBoundary(t *testing.T) {
	const perPage, perDoc = 262144, 6000000
	if maxPageGlyphs != perPage {
		t.Errorf("maxPageGlyphs = %d, want %d", maxPageGlyphs, perPage)
	}
	r := openPDF(t, pagePDF("", "null"))
	if got := r.cache.glyphs.Load(); got != perDoc {
		t.Fatalf("a new Reader may show %d glyphs, want %d", got, perDoc)
	}
	left := perDoc
	for left > 0 {
		n := min(left, perPage)
		g := glyphBudget{r: r}
		if err := failure(t, func() error { g.spend(n); return nil }); err != nil {
			t.Fatalf("%d glyphs shown, %d more: %v", perDoc-left, n, err)
		}
		left -= n
	}
	g := glyphBudget{r: r}
	if err := failure(t, func() error { g.spend(1); return nil }); !errors.Is(err, ErrLimit) {
		t.Errorf("one glyph past %d: got %v, want a panic wrapping ErrLimit", perDoc, err)
	}
}

// TestPageGlyphBoundary verifies a page may show exactly 262,144 glyphs and
// one more is refused with ErrLimit.
func TestPageGlyphBoundary(t *testing.T) {
	g := glyphBudget{}
	if err := failure(t, func() error { g.spend(262144); return nil }); err != nil {
		t.Errorf("262144 glyphs: %v", err)
	}
	if err := failure(t, func() error { g.spend(1); return nil }); !errors.Is(err, ErrLimit) {
		t.Errorf("262145 glyphs: got %v, want a panic wrapping ErrLimit", err)
	}
}
