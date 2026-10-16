package xlsx

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"io"
	"strings"
	"testing"
)

func TestColName(t *testing.T) {
	for n, want := range map[int]string{1: "A", 26: "Z", 27: "AA", 28: "AB", 702: "ZZ", 703: "AAA"} {
		if got := ColName(n); got != want {
			t.Errorf("ColName(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestSheetName(t *testing.T) {
	if got := SheetName(`a/b:c*d?e[f]g\h and a very long tail beyond 31`); strings.ContainsAny(got, `/:*?[]\`) || len([]rune(got)) > 31 {
		t.Errorf("SheetName = %q", got)
	}
}

func TestEscDropsInvalidXML(t *testing.T) {
	if got := esc("a\x00b\x1bc<d>&\"\n"); got != "abc&lt;d&gt;&amp;&quot;\n" {
		t.Errorf("esc = %q", got)
	}
}

func TestWrite_WellFormedParts(t *testing.T) {
	sh := &Sheet{Name: "Liste", FreezeRows: 1, FilterRow: 1, FilterCols: 2, Widths: []float64{10, 20},
		Merges: []string{"A3:B3"}, Heights: map[int]float64{1: 30}}
	sh.Add(Cell{"Nr", Header}, Cell{"Text", Header})
	sh.Add(Cell{1, Number}, Cell{"=SUM(A1) <script>\x07", Wrap})
	sh.Add(Cell{"merged", Muted})
	sh.Validations = []Validation{{Col: 2, FromRow: 2, ToRow: 3, List: []string{"Offen", "Erledigt"}}}
	wb := &Workbook{Sheets: []*Sheet{sh, {Name: "Leer"}}, Title: "T & <x>"}
	var buf bytes.Buffer
	if err := wb.Write(&buf); err != nil {
		t.Fatal(err)
	}
	z, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	parts := map[string]string{}
	for _, f := range z.File {
		rc, _ := f.Open()
		data, _ := io.ReadAll(rc)
		rc.Close()
		dec := xml.NewDecoder(bytes.NewReader(data))
		for {
			if _, err := dec.Token(); err == io.EOF {
				break
			} else if err != nil {
				t.Fatalf("%s: %v", f.Name, err)
			}
		}
		parts[f.Name] = string(data)
	}
	for _, p := range []string{"[Content_Types].xml", "_rels/.rels", "xl/workbook.xml", "xl/styles.xml", "xl/worksheets/sheet1.xml", "xl/worksheets/sheet2.xml"} {
		if _, ok := parts[p]; !ok {
			t.Errorf("missing part %s", p)
		}
	}
	s1 := parts["xl/worksheets/sheet1.xml"]
	// Text is always an inline string, never a formula.
	if strings.Contains(s1, "<f>") || !strings.Contains(s1, `t="inlineStr"`) {
		t.Error("text must be stored as inline strings")
	}
	for _, want := range []string{`<autoFilter ref="A1:B3"/>`, `state="frozen"`, `<mergeCell ref="A3:B3"/>`, `"Offen,Erledigt"`, `ht="30.0"`} {
		if !strings.Contains(s1, want) {
			t.Errorf("sheet1 lacks %s", want)
		}
	}
	if !strings.Contains(parts["xl/workbook.xml"], "_xlnm._FilterDatabase") {
		t.Error("auto-filter needs its defined name")
	}
	// The style count must match the palette.
	if !strings.Contains(parts["xl/styles.xml"], `cellXfs count="20"`) || int(TotalLabel) != 19 {
		t.Error("cellXfs count does not match the Style constants")
	}
}

func TestWrite_RejectsDuplicateSheetNames(t *testing.T) {
	wb := &Workbook{Sheets: []*Sheet{{Name: "A"}, {Name: "a"}}}
	if err := wb.Write(io.Discard); err == nil {
		t.Error("duplicate sheet names must fail")
	}
}
