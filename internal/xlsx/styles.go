package xlsx

import "io"

// The style palette. The order matches cellXfs in stylesXML.
const (
	Default    Style = iota // plain
	Title                   // large bold heading
	Subtitle                // section heading
	Header                  // table header: white on dark blue, wrapped
	Text                    // bordered text, top-aligned
	Wrap                    // bordered wrapped text, top-aligned
	Bold                    // bordered bold text
	Number                  // bordered number with thousands separator
	NumberBold              // bold number
	PrioHigh                // red badge
	PrioMedium              // amber badge
	PrioLow                 // grey badge
	Bad                     // red number (suspicious count)
	Good                    // green wrapped text (legitimate)
	Muted                   // grey italic note, wrapped, no border
	BadText                 // red bold wrapped text
	Label                   // bold on light blue (key/value labels)
	Input                   // light yellow input field, wrapped
	Total                   // bold number on light grey, top border
	TotalLabel              // bold text on light grey
)

// Colors used by the palette (ARGB), exported for tab colors.
const (
	ColorBlue  = "FF1F3864"
	ColorRed   = "FFC00000"
	ColorAmber = "FFBF8F00"
	ColorGrey  = "FF7F7F7F"
	ColorGreen = "FF548235"
)

const stylesXML = xmlHeader + `<styleSheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">` +
	`<fonts count="9">` +
	`<font><sz val="10"/><color rgb="FF262626"/><name val="Calibri"/><family val="2"/></font>` + // 0 default
	`<font><b/><sz val="10"/><color rgb="FF262626"/><name val="Calibri"/><family val="2"/></font>` + // 1 bold
	`<font><b/><sz val="18"/><color rgb="FF1F3864"/><name val="Calibri"/><family val="2"/></font>` + // 2 title
	`<font><b/><sz val="10"/><color rgb="FFFFFFFF"/><name val="Calibri"/><family val="2"/></font>` + // 3 header
	`<font><i/><sz val="10"/><color rgb="FF595959"/><name val="Calibri"/><family val="2"/></font>` + // 4 muted
	`<font><b/><sz val="10"/><color rgb="FF9C0006"/><name val="Calibri"/><family val="2"/></font>` + // 5 red
	`<font><sz val="10"/><color rgb="FF006100"/><name val="Calibri"/><family val="2"/></font>` + // 6 green
	`<font><b/><sz val="10"/><color rgb="FF7F6000"/><name val="Calibri"/><family val="2"/></font>` + // 7 amber
	`<font><b/><sz val="13"/><color rgb="FF1F3864"/><name val="Calibri"/><family val="2"/></font>` + // 8 subtitle
	`</fonts>` +
	`<fills count="10">` +
	`<fill><patternFill patternType="none"/></fill>` + // 0
	`<fill><patternFill patternType="gray125"/></fill>` + // 1 (required)
	`<fill><patternFill patternType="solid"><fgColor rgb="FF1F3864"/><bgColor indexed="64"/></patternFill></fill>` + // 2 header
	`<fill><patternFill patternType="solid"><fgColor rgb="FFFFC7CE"/><bgColor indexed="64"/></patternFill></fill>` + // 3 red
	`<fill><patternFill patternType="solid"><fgColor rgb="FFFFEB9C"/><bgColor indexed="64"/></patternFill></fill>` + // 4 amber
	`<fill><patternFill patternType="solid"><fgColor rgb="FFC6EFCE"/><bgColor indexed="64"/></patternFill></fill>` + // 5 green
	`<fill><patternFill patternType="solid"><fgColor rgb="FFEDEDED"/><bgColor indexed="64"/></patternFill></fill>` + // 6 grey
	`<fill><patternFill patternType="solid"><fgColor rgb="FFDDEBF7"/><bgColor indexed="64"/></patternFill></fill>` + // 7 light blue
	`<fill><patternFill patternType="solid"><fgColor rgb="FFFFF8DC"/><bgColor indexed="64"/></patternFill></fill>` + // 8 input
	`<fill><patternFill patternType="solid"><fgColor rgb="FFF2F2F2"/><bgColor indexed="64"/></patternFill></fill>` + // 9 total
	`</fills>` +
	`<borders count="3">` +
	`<border><left/><right/><top/><bottom/><diagonal/></border>` +
	`<border><left style="thin"><color rgb="FFD9D9D9"/></left><right style="thin"><color rgb="FFD9D9D9"/></right>` +
	`<top style="thin"><color rgb="FFD9D9D9"/></top><bottom style="thin"><color rgb="FFD9D9D9"/></bottom><diagonal/></border>` +
	`<border><left style="thin"><color rgb="FFD9D9D9"/></left><right style="thin"><color rgb="FFD9D9D9"/></right>` +
	`<top style="medium"><color rgb="FF1F3864"/></top><bottom style="thin"><color rgb="FFD9D9D9"/></bottom><diagonal/></border>` +
	`</borders>` +
	`<cellStyleXfs count="1"><xf numFmtId="0" fontId="0" fillId="0" borderId="0"/></cellStyleXfs>` +
	`<cellXfs count="20">` +
	`<xf numFmtId="0" fontId="0" fillId="0" borderId="0" xfId="0"/>` + // Default
	`<xf numFmtId="0" fontId="2" fillId="0" borderId="0" xfId="0" applyFont="1"/>` + // Title
	`<xf numFmtId="0" fontId="8" fillId="0" borderId="0" xfId="0" applyFont="1"/>` + // Subtitle
	`<xf numFmtId="0" fontId="3" fillId="2" borderId="1" xfId="0" applyFont="1" applyFill="1" applyBorder="1" applyAlignment="1"><alignment vertical="center" wrapText="1"/></xf>` + // Header
	`<xf numFmtId="0" fontId="0" fillId="0" borderId="1" xfId="0" applyBorder="1" applyAlignment="1"><alignment vertical="top"/></xf>` + // Text
	`<xf numFmtId="0" fontId="0" fillId="0" borderId="1" xfId="0" applyBorder="1" applyAlignment="1"><alignment vertical="top" wrapText="1"/></xf>` + // Wrap
	`<xf numFmtId="0" fontId="1" fillId="0" borderId="1" xfId="0" applyFont="1" applyBorder="1" applyAlignment="1"><alignment vertical="top" wrapText="1"/></xf>` + // Bold
	`<xf numFmtId="3" fontId="0" fillId="0" borderId="1" xfId="0" applyNumberFormat="1" applyBorder="1" applyAlignment="1"><alignment vertical="top"/></xf>` + // Number
	`<xf numFmtId="3" fontId="1" fillId="0" borderId="1" xfId="0" applyNumberFormat="1" applyFont="1" applyBorder="1" applyAlignment="1"><alignment vertical="top"/></xf>` + // NumberBold
	`<xf numFmtId="0" fontId="5" fillId="3" borderId="1" xfId="0" applyFont="1" applyFill="1" applyBorder="1" applyAlignment="1"><alignment horizontal="center" vertical="top"/></xf>` + // PrioHigh
	`<xf numFmtId="0" fontId="7" fillId="4" borderId="1" xfId="0" applyFont="1" applyFill="1" applyBorder="1" applyAlignment="1"><alignment horizontal="center" vertical="top"/></xf>` + // PrioMedium
	`<xf numFmtId="0" fontId="0" fillId="6" borderId="1" xfId="0" applyFill="1" applyBorder="1" applyAlignment="1"><alignment horizontal="center" vertical="top"/></xf>` + // PrioLow
	`<xf numFmtId="3" fontId="5" fillId="3" borderId="1" xfId="0" applyNumberFormat="1" applyFont="1" applyFill="1" applyBorder="1" applyAlignment="1"><alignment vertical="top"/></xf>` + // Bad
	`<xf numFmtId="0" fontId="6" fillId="5" borderId="1" xfId="0" applyFont="1" applyFill="1" applyBorder="1" applyAlignment="1"><alignment vertical="top" wrapText="1"/></xf>` + // Good
	`<xf numFmtId="0" fontId="4" fillId="0" borderId="0" xfId="0" applyFont="1" applyAlignment="1"><alignment vertical="top" wrapText="1"/></xf>` + // Muted
	`<xf numFmtId="0" fontId="5" fillId="0" borderId="1" xfId="0" applyFont="1" applyBorder="1" applyAlignment="1"><alignment vertical="top" wrapText="1"/></xf>` + // BadText
	`<xf numFmtId="0" fontId="1" fillId="7" borderId="1" xfId="0" applyFont="1" applyFill="1" applyBorder="1" applyAlignment="1"><alignment vertical="top" wrapText="1"/></xf>` + // Label
	`<xf numFmtId="0" fontId="0" fillId="8" borderId="1" xfId="0" applyFill="1" applyBorder="1" applyAlignment="1"><alignment vertical="top" wrapText="1"/></xf>` + // Input
	`<xf numFmtId="3" fontId="1" fillId="9" borderId="2" xfId="0" applyNumberFormat="1" applyFont="1" applyFill="1" applyBorder="1"/>` + // Total
	`<xf numFmtId="0" fontId="1" fillId="9" borderId="2" xfId="0" applyFont="1" applyFill="1" applyBorder="1"/>` + // TotalLabel
	`</cellXfs>` +
	`<cellStyles count="1"><cellStyle name="Normal" xfId="0" builtinId="0"/></cellStyles>` +
	`</styleSheet>`

func writeStyles(w io.Writer) error {
	_, err := io.WriteString(w, stylesXML)
	return err
}
