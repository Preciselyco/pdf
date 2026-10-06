package pdf

import (
	"bytes"
	"compress/zlib"
	"errors"
	"fmt"
	"io"
	"runtime"
	"strings"
	"testing"
)

// failure runs fn and returns the error it returns, or the error it panics
// with. A panic that carries anything but an error is reported as a failure
// of its own: callers of the methods that panic recover a value and test it
// with errors.Is.
func failure(t *testing.T, fn func() error) (err error) {
	t.Helper()
	defer func() {
		if p := recover(); p != nil {
			if _, ok := p.(runtime.Error); ok {
				panic(p)
			}
			e, ok := p.(error)
			if !ok {
				err = fmt.Errorf("panic with %T, not an error: %v", p, p)
				return
			}
			err = e
		}
	}()
	return fn()
}

// TestErrLimitWrapped walks hostile inputs that trip each limit and checks
// the error, or the recovered panic value, wraps ErrLimit.
func TestErrLimitWrapped(t *testing.T) {
	nop := func(*Stack, string) {}
	fullTable := func() *xrefTable {
		table := newXrefTable(0)
		table.n = maxXrefEntries
		return table
	}
	xrefStream := func(w array, data string) error {
		r := &Reader{f: bytes.NewReader([]byte(data)), end: int64(len(data))}
		hdr := dict{name("Length"): int64(len(data)), name("W"): w, name("Index"): array{int64(1), int64(1)}}
		_, err := readXrefStreamData(r, stream{hdr, objptr{}, 0}, newXrefTable(0), 2)
		return err
	}
	// streamOf returns the error reading the /Contents of a one-page file
	// whose second object is stream with header hdr and body.
	streamOf := func(hdr, body string, objs ...string) func() error {
		return func() error {
			r := openPDF(t, pagePDF("", append([]string{
				fmt.Sprintf("<< /Length %d %s >>\nstream\n%s\nendstream", len(body), hdr, body),
			}, objs...)...))
			_, err := io.Copy(io.Discard, r.Page(1).V.Key("Contents").Reader())
			return err
		}
	}
	var asciiChain strings.Builder
	for range maxFilters + 1 {
		asciiChain.WriteString("/ASCII85Decode ")
	}
	budgetBomb := func() []byte {
		var z bytes.Buffer
		zw := zlib.NewWriter(&z)
		row := bytes.Repeat([]byte{2}, 1<<20)
		for range minDecodeBudget>>20 + 1 {
			zw.Write(row)
		}
		zw.Close()
		return z.Bytes()
	}()
	objStmIndexed := func() error {
		data := xrefStreamFile(testObj{num: 2, hdr: "/N NUM /First FIRST", members: []testObj{
			{num: 3, body: "null"},
			{num: 1, body: "<< /Type /Catalog /Pages << /Type /Pages /Kids [] /Count 7 >> >>"},
		}})
		r := openPDF(t, data)
		r.cache.indexed.Store(maxXrefEntries - 1)
		declaredPages(r)
		return nil
	}
	extends := func() error {
		const catalog = "<< /Type /Catalog /Pages << /Type /Pages /Kids [] /Count 7 >> >>"
		n := maxObjStmExtends + 1
		var objs []testObj
		for i := range n {
			hdr, member := "/N NUM /First FIRST", testObj{num: 1, body: catalog}
			if i < n-1 {
				hdr += fmt.Sprintf(" /Extends %d 0 R", 101+i)
				member = testObj{num: 200 + i, body: "null"}
			}
			objs = append(objs, testObj{num: 100 + i, hdr: hdr, members: []testObj{member}})
		}
		declaredPages(openPDF(t, xrefStreamFile(append(objs, testObj{num: 1, in: 100})...)))
		return nil
	}
	cyclicExtends := func() error {
		// A stream extending itself never holds the object.
		objs := []testObj{
			{num: 100, hdr: "/N NUM /First FIRST /Extends 100 0 R", members: []testObj{{num: 200, body: "null"}}},
			{num: 1, in: 100},
		}
		declaredPages(openPDF(t, xrefStreamFile(objs...)))
		return nil
	}

	tests := []struct {
		name string
		fn   func() error
	}{
		{"xref table entries", func() error {
			b := newBuffer(strings.NewReader("1 1\n0000000009 00000 n \ntrailer"), 0)
			_, err := readXrefTableData(b, fullTable())
			return err
		}},
		{"xref table rows", func() error {
			table := newXrefTable(0)
			table.rows = maxXrefRows
			b := newBuffer(strings.NewReader("1 1\n0000000009 00000 n \ntrailer"), 0)
			_, err := readXrefTableData(b, table)
			return err
		}},
		{"xref stream entries", func() error {
			data := "\x01\x09\x00"
			r := &Reader{f: bytes.NewReader([]byte(data)), end: int64(len(data))}
			hdr := dict{name("Length"): int64(len(data)), name("W"): array{int64(1), int64(1), int64(1)}, name("Index"): array{int64(1), int64(1)}}
			_, err := readXrefStreamData(r, stream{hdr, objptr{}, 0}, fullTable(), 2)
			return err
		}},
		{"xref stream rows", func() error {
			table := newXrefTable(0)
			table.rows = maxXrefRows
			data := "\x01\x09\x00"
			r := &Reader{f: bytes.NewReader([]byte(data)), end: int64(len(data))}
			hdr := dict{name("Length"): int64(len(data)), name("W"): array{int64(1), int64(1), int64(1)}, name("Index"): array{int64(1), int64(1)}}
			_, err := readXrefStreamData(r, stream{hdr, objptr{}, 0}, table, 2)
			return err
		}},
		{"xref /W field width", func() error {
			return xrefStream(array{int64(maxXrefFieldWidth + 1), int64(1), int64(1)}, "\x00")
		}},
		{"compressed xref stream entries", func() error {
			const rows = 1 << 23
			comp := deflate(bytes.Repeat([]byte{1, 9, 0}, rows))
			return openBytes(xrefStreamPDF(fmt.Sprintf("/Size %d /W [1 1 1] /Filter /FlateDecode", rows), string(comp)))
		}},
		{"predictor /Columns", streamOf(
			fmt.Sprintf("/Filter /FlateDecode /DecodeParms << /Predictor 12 /Columns %d >>", maxPredictorColumns+1),
			string(deflate([]byte("x"))))},
		{"filter count", streamOf("/Filter 5 0 R", "x", "["+asciiChain.String()+"]")},
		{"decode budget", streamOf("/Filter /FlateDecode /DecodeParms << /Predictor 12 >>", string(budgetBomb))},
		{"decode budget spent", func() error {
			r := openPDF(t, pagePDF("", "null"))
			return r.cache.spend(r.cache.limit + 1)
		}},
		{"object stream bytes", func() error {
			declaredPages(openPDF(t, objStmFile(maxObjStmBytes+1)))
			return nil
		}},
		{"object stream index", objStmIndexed},
		{"object stream /Extends chain", extends},
		{"object stream /Extends cycle", cyclicExtends},
		{"Interpret bytes", func() error {
			_, err := openPDF(t, pagePDF("", flateObj(strings.Repeat(" ", maxInterpretBytes+1)))).Page(1).GetPlainText(nil)
			return err
		}},
		{"Interpret operands", func() error {
			Interpret(rawStream(strings.Repeat("1 ", maxOperands+1)), nop)
			return nil
		}},
		{"Interpret dict stack", func() error {
			Interpret(rawStream(strings.Repeat("<<>> begin ", maxDictStack+1)), nop)
			return nil
		}},
		{"Interpret malformed tokens", func() error {
			Interpret(rawStream(strings.Repeat("[) ", maxInterpretErrors+1)), nop)
			return nil
		}},
		{"object nesting", func() error {
			newBuffer(strings.NewReader(strings.Repeat("[", maxObjectDepth+10)), 0).readObject()
			return nil
		}},
		{"object entries", func() error {
			newBuffer(strings.NewReader("["+strings.Repeat("0 ", maxOperands+1)+"]"), 0).readObject()
			return nil
		}},
		{"ToUnicode cmap bytes", func() error {
			readCmap(rawStream(strings.Repeat(" ", maxInterpretBytes+1)))
			return nil
		}},
		{"glyphs per page", func() error {
			pageWithContent("BT (" + strings.Repeat("A", maxPageGlyphs+1) + ") Tj ET").Content()
			return nil
		}},
		{"glyphs per page, GetPlainText", func() error {
			_, err := pageWithContent("BT (" + strings.Repeat("A", maxPageGlyphs+1) + ") Tj ET").GetPlainText(nil)
			return err
		}},
		{"glyphs per document", func() error {
			r := openPDF(t, pagePDF("", "null"))
			r.cache.glyphs.Store(10)
			g := glyphBudget{r: r}
			g.spend(11)
			return nil
		}},
		{"glyphs per document, GetPlainText", func() error {
			r := openPDF(t, pagePDF("", flateObj("BT (AAAAAAAAAAAA) Tj ET")))
			r.cache.glyphs.Store(10)
			_, err := r.GetPlainText()
			return err
		}},
		{"glyphs per document, GetStyledTexts", func() error {
			r := openPDF(t, pagePDF("", flateObj("BT (AAAAAAAAAAAA) Tj ET")))
			r.cache.glyphs.Store(10)
			_, err := r.GetStyledTexts()
			return err
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := failure(t, tt.fn)
			if err == nil {
				t.Fatal("no error or panic, want one wrapping ErrLimit")
			}
			if !errors.Is(err, ErrLimit) {
				t.Errorf("got %v, which does not wrap ErrLimit", err)
			}
		})
	}
}

// TestMalformedIsNotLimit checks that malformed input, which no limit
// explains, does not wrap ErrLimit.
func TestMalformedIsNotLimit(t *testing.T) {
	tests := []struct {
		name string
		fn   func() error
	}{
		{"not a PDF", func() error { return openBytes([]byte("hello, world, this is not a PDF")) }},
		{"negative /W width", func() error {
			data := "\x00"
			r := &Reader{f: bytes.NewReader([]byte(data)), end: int64(len(data))}
			hdr := dict{name("Length"): int64(len(data)), name("W"): array{int64(-1), int64(1), int64(1)}, name("Index"): array{int64(1), int64(1)}}
			_, err := readXrefStreamData(r, stream{hdr, objptr{}, 0}, newXrefTable(0), 2)
			return err
		}},
		{"zero predictor /Columns", func() error {
			r := openPDF(t, pagePDF("", fmt.Sprintf("<< /Length 1 /Filter /FlateDecode /DecodeParms << /Predictor 12 /Columns 0 >> >>\nstream\n%s\nendstream", deflate([]byte("x")))))
			_, err := io.Copy(io.Discard, r.Page(1).V.Key("Contents").Reader())
			return err
		}},
		{"unterminated hex string", func() error {
			newBuffer(strings.NewReader("<zz>"), 0).readObject()
			return nil
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := failure(t, tt.fn)
			if err == nil {
				t.Fatal("no error")
			}
			if errors.Is(err, ErrLimit) {
				t.Errorf("got %v, which wraps ErrLimit", err)
			}
		})
	}
}
