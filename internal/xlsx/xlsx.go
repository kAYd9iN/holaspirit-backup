// Package xlsx writes formatted Office Open XML workbooks (.xlsx) with the
// standard library only: inline strings, a fixed style palette, column
// widths, frozen panes, auto-filters, list validations, merged cells and
// print settings. It writes; it does not read.
package xlsx

import (
	"archive/zip"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Style selects one of the predefined cell formats (see styles.go).
type Style int

// Cell is one cell value: string, int, int64 or float64. A nil Value with a
// style still renders the style (borders, fill).
type Cell struct {
	Value any
	Style Style
}

// Validation restricts a column range to a list of values (drop-down).
type Validation struct {
	Col            int // 1-based
	FromRow, ToRow int // 1-based, inclusive
	List           []string
}

// Sheet is one worksheet.
type Sheet struct {
	Name     string
	TabColor string // ARGB, e.g. "FF1F3864"; "" = none
	Widths   []float64
	Rows     [][]Cell
	Heights  map[int]float64 // 1-based row → height in points
	// FreezeRows/FreezeCols keep the first rows/columns visible.
	FreezeRows, FreezeCols int
	// FilterRow (1-based) is the header row of an auto-filtered table that
	// spans all following rows; 0 = no filter.
	FilterRow   int
	FilterCols  int
	Validations []Validation
	Merges      []string // "A1:F1"
	Landscape   bool
}

// Add appends a row and returns its 1-based number.
func (s *Sheet) Add(cells ...Cell) int {
	s.Rows = append(s.Rows, cells)
	return len(s.Rows)
}

// Workbook is a set of sheets.
type Workbook struct {
	Sheets  []*Sheet
	Title   string
	Creator string
	Created time.Time
}

// MaxCellChars is Excel's limit for one cell.
const MaxCellChars = 32767

// ColName converts a 1-based column number to its letters (1 → A, 28 → AB).
func ColName(n int) string {
	s := ""
	for n > 0 {
		n--
		s = string(rune('A'+n%26)) + s
		n /= 26
	}
	return s
}

// Ref returns an A1 reference.
func Ref(col, row int) string { return ColName(col) + strconv.Itoa(row) }

// SheetName makes a valid, unique-enough sheet name: no []:*?/\, ≤ 31 chars.
func SheetName(s string) string {
	s = strings.Map(func(r rune) rune {
		if strings.ContainsRune(`[]:*?/\`, r) {
			return '-'
		}
		return r
	}, s)
	if utf8.RuneCountInString(s) > 31 {
		s = string([]rune(s)[:31])
	}
	return s
}

// Write writes the workbook as .xlsx to w.
func (wb *Workbook) Write(w io.Writer) error {
	if len(wb.Sheets) == 0 {
		return fmt.Errorf("xlsx: workbook has no sheets")
	}
	seen := map[string]bool{}
	for _, s := range wb.Sheets {
		s.Name = SheetName(s.Name)
		k := strings.ToLower(s.Name)
		if seen[k] || s.Name == "" {
			return fmt.Errorf("xlsx: duplicate or empty sheet name %q", s.Name)
		}
		seen[k] = true
	}
	z := zip.NewWriter(w)
	files := []struct {
		name string
		body func(io.Writer) error
	}{
		{"[Content_Types].xml", wb.contentTypes},
		{"_rels/.rels", rootRels},
		{"docProps/core.xml", wb.core},
		{"docProps/app.xml", appProps},
		{"xl/workbook.xml", wb.workbook},
		{"xl/_rels/workbook.xml.rels", wb.workbookRels},
		{"xl/styles.xml", writeStyles},
	}
	for _, f := range files {
		fw, err := z.Create(f.name)
		if err != nil {
			return err
		}
		if err := f.body(fw); err != nil {
			return err
		}
	}
	for i, s := range wb.Sheets {
		fw, err := z.Create(fmt.Sprintf("xl/worksheets/sheet%d.xml", i+1))
		if err != nil {
			return err
		}
		if err := s.write(fw); err != nil {
			return err
		}
	}
	return z.Close()
}

const xmlHeader = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n"

func (wb *Workbook) contentTypes(w io.Writer) error {
	var b strings.Builder
	b.WriteString(xmlHeader)
	b.WriteString(`<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">`)
	b.WriteString(`<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>`)
	b.WriteString(`<Default Extension="xml" ContentType="application/xml"/>`)
	b.WriteString(`<Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/>`)
	b.WriteString(`<Override PartName="/xl/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.styles+xml"/>`)
	b.WriteString(`<Override PartName="/docProps/core.xml" ContentType="application/vnd.openxmlformats-package.core-properties+xml"/>`)
	b.WriteString(`<Override PartName="/docProps/app.xml" ContentType="application/vnd.openxmlformats-officedocument.extended-properties+xml"/>`)
	for i := range wb.Sheets {
		fmt.Fprintf(&b, `<Override PartName="/xl/worksheets/sheet%d.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/>`, i+1)
	}
	b.WriteString(`</Types>`)
	_, err := io.WriteString(w, b.String())
	return err
}

func rootRels(w io.Writer) error {
	_, err := io.WriteString(w, xmlHeader+`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">`+
		`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/>`+
		`<Relationship Id="rId2" Type="http://schemas.openxmlformats.org/package/2006/relationships/metadata/core-properties" Target="docProps/core.xml"/>`+
		`<Relationship Id="rId3" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/extended-properties" Target="docProps/app.xml"/>`+
		`</Relationships>`)
	return err
}

func (wb *Workbook) core(w io.Writer) error {
	created := wb.Created
	if created.IsZero() {
		created = time.Now()
	}
	_, err := io.WriteString(w, xmlHeader+`<cp:coreProperties xmlns:cp="http://schemas.openxmlformats.org/package/2006/metadata/core-properties" `+
		`xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:dcterms="http://purl.org/dc/terms/" `+
		`xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance">`+
		`<dc:title>`+esc(wb.Title)+`</dc:title><dc:creator>`+esc(wb.Creator)+`</dc:creator>`+
		`<dcterms:created xsi:type="dcterms:W3CDTF">`+created.UTC().Format(time.RFC3339)+`</dcterms:created>`+
		`</cp:coreProperties>`)
	return err
}

func appProps(w io.Writer) error {
	_, err := io.WriteString(w, xmlHeader+`<Properties xmlns="http://schemas.openxmlformats.org/officeDocument/2006/extended-properties"><Application>talkspirit-backup</Application></Properties>`)
	return err
}

func (wb *Workbook) workbook(w io.Writer) error {
	var b strings.Builder
	b.WriteString(xmlHeader)
	b.WriteString(`<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">`)
	b.WriteString(`<bookViews><workbookView activeTab="0"/></bookViews><sheets>`)
	for i, s := range wb.Sheets {
		fmt.Fprintf(&b, `<sheet name="%s" sheetId="%d" r:id="rId%d"/>`, esc(s.Name), i+1, i+1)
	}
	b.WriteString(`</sheets>`)
	var names strings.Builder
	for i, s := range wb.Sheets {
		quoted := "'" + strings.ReplaceAll(s.Name, "'", "''") + "'"
		if s.FilterRow > 0 && s.FilterCols > 0 {
			last := len(s.Rows)
			if last < s.FilterRow {
				last = s.FilterRow
			}
			fmt.Fprintf(&names, `<definedName name="_xlnm._FilterDatabase" localSheetId="%d" hidden="1">%s!$A$%d:$%s$%d</definedName>`,
				i, esc(quoted), s.FilterRow, ColName(s.FilterCols), last)
			fmt.Fprintf(&names, `<definedName name="_xlnm.Print_Titles" localSheetId="%d">%s!$%d:$%d</definedName>`,
				i, esc(quoted), s.FilterRow, s.FilterRow)
		}
	}
	if names.Len() > 0 {
		b.WriteString(`<definedNames>` + names.String() + `</definedNames>`)
	}
	b.WriteString(`</workbook>`)
	_, err := io.WriteString(w, b.String())
	return err
}

func (wb *Workbook) workbookRels(w io.Writer) error {
	var b strings.Builder
	b.WriteString(xmlHeader)
	b.WriteString(`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">`)
	for i := range wb.Sheets {
		fmt.Fprintf(&b, `<Relationship Id="rId%d" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet%d.xml"/>`, i+1, i+1)
	}
	fmt.Fprintf(&b, `<Relationship Id="rId%d" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/>`, len(wb.Sheets)+1)
	b.WriteString(`</Relationships>`)
	_, err := io.WriteString(w, b.String())
	return err
}

func (s *Sheet) write(w io.Writer) error {
	b := &strings.Builder{}
	b.WriteString(xmlHeader)
	b.WriteString(`<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">`)
	b.WriteString(`<sheetPr>`)
	if s.TabColor != "" {
		fmt.Fprintf(b, `<tabColor rgb="%s"/>`, esc(s.TabColor))
	}
	b.WriteString(`<pageSetUpPr fitToPage="1"/></sheetPr>`)

	b.WriteString(`<sheetViews><sheetView workbookViewId="0" zoomScale="100">`)
	if s.FreezeRows > 0 || s.FreezeCols > 0 {
		pane := "bottomRight"
		switch {
		case s.FreezeCols == 0:
			pane = "bottomLeft"
		case s.FreezeRows == 0:
			pane = "topRight"
		}
		b.WriteString(`<pane`)
		if s.FreezeCols > 0 {
			fmt.Fprintf(b, ` xSplit="%d"`, s.FreezeCols)
		}
		if s.FreezeRows > 0 {
			fmt.Fprintf(b, ` ySplit="%d"`, s.FreezeRows)
		}
		fmt.Fprintf(b, ` topLeftCell="%s" activePane="%s" state="frozen"/><selection pane="%s"/>`,
			Ref(s.FreezeCols+1, s.FreezeRows+1), pane, pane)
	}
	b.WriteString(`</sheetView></sheetViews>`)
	b.WriteString(`<sheetFormatPr defaultRowHeight="15"/>`)

	if len(s.Widths) > 0 {
		b.WriteString(`<cols>`)
		for i, wd := range s.Widths {
			if wd > 0 {
				fmt.Fprintf(b, `<col min="%d" max="%d" width="%.2f" customWidth="1"/>`, i+1, i+1, wd)
			}
		}
		b.WriteString(`</cols>`)
	}

	b.WriteString(`<sheetData>`)
	for ri, row := range s.Rows {
		r := ri + 1
		if h, ok := s.Heights[r]; ok {
			fmt.Fprintf(b, `<row r="%d" ht="%.1f" customHeight="1">`, r, h)
		} else {
			fmt.Fprintf(b, `<row r="%d">`, r)
		}
		for ci, c := range row {
			writeCell(b, Ref(ci+1, r), c)
		}
		b.WriteString(`</row>`)
	}
	b.WriteString(`</sheetData>`)

	if s.FilterRow > 0 && s.FilterCols > 0 {
		last := len(s.Rows)
		if last < s.FilterRow {
			last = s.FilterRow
		}
		fmt.Fprintf(b, `<autoFilter ref="A%d:%s"/>`, s.FilterRow, Ref(s.FilterCols, last))
	}
	if len(s.Merges) > 0 {
		fmt.Fprintf(b, `<mergeCells count="%d">`, len(s.Merges))
		for _, m := range s.Merges {
			fmt.Fprintf(b, `<mergeCell ref="%s"/>`, esc(m))
		}
		b.WriteString(`</mergeCells>`)
	}
	var vals []Validation
	for _, v := range s.Validations {
		if v.ToRow >= v.FromRow && len(v.List) > 0 {
			vals = append(vals, v)
		}
	}
	if len(vals) > 0 {
		fmt.Fprintf(b, `<dataValidations count="%d">`, len(vals))
		for _, v := range vals {
			fmt.Fprintf(b, `<dataValidation type="list" allowBlank="1" showErrorMessage="1" sqref="%s:%s"><formula1>"%s"</formula1></dataValidation>`,
				Ref(v.Col, v.FromRow), Ref(v.Col, v.ToRow), esc(strings.Join(v.List, ",")))
		}
		b.WriteString(`</dataValidations>`)
	}
	b.WriteString(`<pageMargins left="0.4" right="0.4" top="0.5" bottom="0.5" header="0.3" footer="0.3"/>`)
	orientation := "portrait"
	if s.Landscape {
		orientation = "landscape"
	}
	fmt.Fprintf(b, `<pageSetup paperSize="9" orientation="%s" fitToWidth="1" fitToHeight="0"/>`, orientation)
	b.WriteString(`</worksheet>`)
	_, err := io.WriteString(w, b.String())
	return err
}

func writeCell(b *strings.Builder, ref string, c Cell) {
	switch v := c.Value.(type) {
	case nil:
		if c.Style != 0 {
			fmt.Fprintf(b, `<c r="%s" s="%d"/>`, ref, c.Style)
		}
	case int:
		fmt.Fprintf(b, `<c r="%s" s="%d"><v>%d</v></c>`, ref, c.Style, v)
	case int64:
		fmt.Fprintf(b, `<c r="%s" s="%d"><v>%d</v></c>`, ref, c.Style, v)
	case float64:
		fmt.Fprintf(b, `<c r="%s" s="%d"><v>%s</v></c>`, ref, c.Style, strconv.FormatFloat(v, 'f', -1, 64))
	default:
		s := fmt.Sprint(v)
		if s == "" && c.Style == 0 {
			return
		}
		if utf8.RuneCountInString(s) > MaxCellChars {
			s = string([]rune(s)[:MaxCellChars-1]) + "…"
		}
		fmt.Fprintf(b, `<c r="%s" s="%d" t="inlineStr"><is><t xml:space="preserve">%s</t></is></c>`, ref, c.Style, esc(s))
	}
}

// esc escapes text for XML and drops characters XML 1.0 does not allow.
func esc(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '&':
			b.WriteString("&amp;")
		case r == '<':
			b.WriteString("&lt;")
		case r == '>':
			b.WriteString("&gt;")
		case r == '"':
			b.WriteString("&quot;")
		case r == '\t' || r == '\n' || r == '\r':
			b.WriteRune(r)
		case r < 0x20 || (r >= 0xD800 && r <= 0xDFFF) || r == 0xFFFE || r == 0xFFFF || r == utf8.RuneError:
			// not representable in XML 1.0
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
