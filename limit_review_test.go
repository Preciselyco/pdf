package pdf

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

// helloPage returns a one-page file whose content is content followed by a
// line of text.
func helloPage(content string) *Reader {
	return openPDFNoT(pagePDF("/Resources << /Font << /F1 5 0 R >> >>",
		streamObj(content+" BT /F1 12 Tf (Hello) Tj ET"),
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>"))
}

func openPDFNoT(data []byte) *Reader {
	r, err := NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		panic(err)
	}
	return r
}

// TestContentLimitNotSwallowed verifies a limit tripped parsing one operand
// of a content stream fails the page, instead of being skipped as a
// malformed operand with the text after it extracted.
func TestContentLimitNotSwallowed(t *testing.T) {
	for _, tt := range []struct{ name, content string }{
		{"object depth", strings.Repeat("[", maxObjectDepth+5) + strings.Repeat("]", maxObjectDepth+5)},
		{"object entries", "[" + strings.Repeat("0 ", maxOperands+1) + "]"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var text string
			err := failure(t, func() (err error) {
				text, err = helloPage(tt.content).Page(1).GetPlainText(nil)
				return err
			})
			if !errors.Is(err, ErrLimit) {
				t.Errorf("GetPlainText = %q, %v; want an error wrapping ErrLimit", text, err)
			}
		})
	}
}

// TestPageTreeCapsFailClosed verifies a page tree past the node or depth cap
// is refused, not read as listing fewer pages.
func TestPageTreeCapsFailClosed(t *testing.T) {
	t.Run("nodes", func(t *testing.T) {
		const nodes, perNode = 5, 250000
		objs := []string{"<< /Type /Catalog /Pages 2 0 R >>", ""}
		var kids strings.Builder
		for i := range nodes {
			fmt.Fprintf(&kids, "%d 0 R ", 3+i)
			objs = append(objs, "<< /Type /Pages /Parent 2 0 R /Kids ["+strings.Repeat("<< >> ", perNode)+"] >>")
		}
		fmt.Fprintf(&kids, "%d 0 R", 3+nodes)
		objs = append(objs, "<< /Type /Page /Parent 2 0 R >>")
		objs[1] = "<< /Type /Pages /Kids [" + kids.String() + "] >>"
		r := openPDF(t, buildPDF(objs...))
		got := 0
		if err := failure(t, func() error { got = r.NumPage(); return nil }); !errors.Is(err, ErrLimit) {
			t.Errorf("NumPage = %d, %v; want a panic wrapping ErrLimit", got, err)
		}
	})
	t.Run("depth", func(t *testing.T) {
		const depth = maxPageTreeDepth + 2
		objs := []string{"<< /Type /Catalog /Pages 2 0 R >>"}
		for i := range depth {
			objs = append(objs, fmt.Sprintf("<< /Type /Pages /Kids [%d 0 R] >>", 3+i))
		}
		objs = append(objs, "<< /Type /Page >>")
		r := openPDF(t, buildPDF(objs...))
		got := 0
		if err := failure(t, func() error { got = r.NumPage(); return nil }); !errors.Is(err, ErrLimit) {
			t.Errorf("NumPage = %d, %v; want a panic wrapping ErrLimit", got, err)
		}
	})
}

// TestMalformedCmapStillDegrades verifies a cmap that is only malformed, by
// more stray tokens than Interpret skips, reads as no cmap and the page text
// is still extracted.
func TestMalformedCmapStillDegrades(t *testing.T) {
	cmap := strings.Repeat("[) ", maxInterpretErrors+1) + "1 beginbfchar <41> <0058> endbfchar"
	r := openPDF(t, pagePDF("/Resources << /Font << /F1 5 0 R >> >>",
		streamObj("BT /F1 12 Tf (A) Tj ET"),
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /ToUnicode 6 0 R >>",
		streamObj(cmap),
	))
	text, err := r.Page(1).GetPlainText(nil)
	if err != nil || !strings.Contains(text, "A") {
		t.Errorf("GetPlainText = %q, %v; want the text with a nil error", text, err)
	}
}

// glyphsPDF returns a file of n pages sharing one content stream of glyphs
// glyphs.
func glyphsPDF(n, glyphs int) []byte {
	objs := []string{"<< /Type /Catalog /Pages 2 0 R >>", "",
		streamObj("BT /F1 12 Tf (" + strings.Repeat("A", glyphs) + ") Tj ET"),
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>"}
	var kids strings.Builder
	for i := range n {
		fmt.Fprintf(&kids, "%d 0 R ", 5+i)
		objs = append(objs, "<< /Type /Page /Parent 2 0 R /Contents 3 0 R /Resources << /Font << /F1 4 0 R >> >> >>")
	}
	objs[1] = fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", kids.String(), n)
	return buildPDF(objs...)
}

// TestGlyphsChargedOncePerPage verifies the document glyph budget counts what
// a page shows, however many times it is extracted.
func TestGlyphsChargedOncePerPage(t *testing.T) {
	t.Run("same page again", func(t *testing.T) {
		r := openPDF(t, glyphsPDF(1, 1000))
		if _, err := r.Page(1).GetPlainText(nil); err != nil {
			t.Fatal(err)
		}
		after := r.cache.glyphs.Load()
		r.Page(1).GetPlainText(nil)
		r.Page(1).Content()
		if after == maxDocGlyphs {
			t.Fatal("the first extraction charged nothing")
		}
		if got := r.cache.glyphs.Load(); got < after-1100 {
			t.Errorf("re-extracting charged %d more glyphs of a 1000-glyph page", after-got)
		}
	})
	t.Run("twice over a long contract", func(t *testing.T) {
		r := openPDF(t, glyphsPDF(1100, 3000))
		for i := 1; i <= r.NumPage(); i++ {
			if _, err := r.Page(i).GetPlainText(nil); err != nil {
				t.Fatalf("page %d GetPlainText: %v", i, err)
			}
			if err := failure(t, func() error { r.Page(i).Content(); return nil }); err != nil {
				t.Fatalf("page %d Content: %v", i, err)
			}
		}
	})
	t.Run("over the limit", func(t *testing.T) {
		r := openPDF(t, glyphsPDF(2000, 3001))
		var err error
		for i := 1; i <= r.NumPage() && err == nil; i++ {
			_, err = r.Page(i).GetPlainText(nil)
		}
		if !errors.Is(err, ErrLimit) {
			t.Errorf("2000 pages of 3001 glyphs: got %v, want ErrLimit", err)
		}
	})
}

// TestLimitMessageNotMalformed verifies a limit is not reported as a
// malformed PDF.
func TestLimitMessageNotMalformed(t *testing.T) {
	r := openPDF(t, flatPages(maxPages+1))
	_, err := r.GetPlainText()
	if !errors.Is(err, ErrLimit) {
		t.Fatalf("GetPlainText: got %v, want ErrLimit", err)
	}
	if strings.Contains(err.Error(), "malformed") {
		t.Errorf("GetPlainText: %q reads as a malformed file", err)
	}
	_, err = r.GetStyledTexts()
	if !errors.Is(err, ErrLimit) || strings.Contains(err.Error(), "malformed") {
		t.Errorf("GetStyledTexts: got %q, want a limit that does not read as malformed", err)
	}
}

// TestInlinePagesChargedInFull verifies pages written as direct dicts in
// /Kids, which have no object of their own, are each charged against the
// document glyph budget.
func TestInlinePagesChargedInFull(t *testing.T) {
	const pages, glyphs = 30, 250000
	var kids strings.Builder
	for range pages {
		kids.WriteString("<< /Type /Page /Contents 3 0 R /Resources << /Font << /F1 4 0 R >> >> >> ")
	}
	r := openPDF(t, buildPDF(
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids ["+kids.String()+"] /Count 30 >>",
		streamObj("BT /F1 12 Tf ("+strings.Repeat("A", glyphs)+") Tj ET"),
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
	))
	var err error
	shown := 0
	for i := 1; i <= r.NumPage() && err == nil; i++ {
		_, err = r.Page(i).GetPlainText(nil)
		shown += glyphs
	}
	if !errors.Is(err, ErrLimit) {
		t.Errorf("%d inline pages of %d glyphs: got %v after %d glyphs, want ErrLimit", pages, glyphs, err, shown)
	}
}

// TestPaidPageSurvivesExhaustion verifies a page already charged can be
// extracted again once the document budget is spent.
func TestPaidPageSurvivesExhaustion(t *testing.T) {
	r := openPDF(t, glyphsPDF(2, 1000))
	if _, err := r.Page(1).GetPlainText(nil); err != nil {
		t.Fatal(err)
	}
	r.cache.glyphs.Store(500)
	if _, err := r.Page(2).GetPlainText(nil); !errors.Is(err, ErrLimit) {
		t.Fatalf("page 2: got %v, want ErrLimit", err)
	}
	if _, err := r.Page(1).GetPlainText(nil); err != nil {
		t.Errorf("page 1 again after exhaustion: %v", err)
	}
}

// TestGlyphChargeConcurrent verifies goroutines extracting one page together
// charge it once.
func TestGlyphChargeConcurrent(t *testing.T) {
	data := glyphsPDF(1, 2000)
	one := openPDF(t, data)
	one.Page(1).GetPlainText(nil)
	want := one.cache.glyphs.Load()

	r := openPDF(t, data)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.Page(1).GetPlainText(nil)
			r.Page(1).Content()
		}()
	}
	wg.Wait()
	if got := r.cache.glyphs.Load(); got != want {
		t.Errorf("8 goroutines left %d glyphs, one extraction leaves %d", got, want)
	}
}
