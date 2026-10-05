// Package xlsx writes minimal, valid .xlsx workbooks using only the standard
// library. It is deliberately small: every cell is an inline string, which is
// enough for the attendance export and avoids any external dependency.
package xlsx

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Sheet is one worksheet: a name, a header row and data rows.
type Sheet struct {
	Name   string
	Header []string
	Rows   [][]string
	Widths []float64 // optional column widths (indexed by column)
}

const (
	contentTypesRel = "application/vnd.openxmlformats-package.relationships+xml"
	contentTypeXML  = "application/xml"
	sheetMainCT     = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"
	sheetCT         = "application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"
	stylesCT        = "application/vnd.openxmlformats-officedocument.spreadsheetml.styles+xml"

	relNS    = "http://schemas.openxmlformats.org/package/2006/relationships"
	relDoc   = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument"
	relSheet = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet"
	relStyle = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles"

	mainNS = "http://schemas.openxmlformats.org/spreadsheetml/2006/main"
)

// Write builds or overwrites the workbook at path. Sheets are written in the
// given order.
func Write(path string, sheets []Sheet) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	// [Content_Types].xml
	{
		var b strings.Builder
		b.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`)
		b.WriteString(`<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">`)
		b.WriteString(`<Default Extension="rels" ContentType="` + contentTypesRel + `"/>`)
		b.WriteString(`<Default Extension="xml" ContentType="` + contentTypeXML + `"/>`)
		b.WriteString(`<Override PartName="/xl/workbook.xml" ContentType="` + sheetMainCT + `"/>`)
		b.WriteString(`<Override PartName="/xl/styles.xml" ContentType="` + stylesCT + `"/>`)
		for i := range sheets {
			fmt.Fprintf(&b, `<Override PartName="/xl/worksheets/sheet%d.xml" ContentType="%s"/>`, i+1, sheetCT)
		}
		b.WriteString(`</Types>`)
		if err := addFile(zw, "[Content_Types].xml", b.String()); err != nil {
			return err
		}
	}

	// _rels/.rels
	{
		var b strings.Builder
		b.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`)
		fmt.Fprintf(&b, `<Relationships xmlns="%s"><Relationship Id="rId1" Type="%s" Target="xl/workbook.xml"/></Relationships>`, relNS, relDoc)
		if err := addFile(zw, "_rels/.rels", b.String()); err != nil {
			return err
		}
	}

	// xl/workbook.xml
	{
		var b strings.Builder
		b.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`)
		fmt.Fprintf(&b, `<workbook xmlns="%s" xmlns:r="%s"><sheets>`, mainNS, relNS)
		for i, s := range sheets {
			fmt.Fprintf(&b, `<sheet name="%s" sheetId="%d" r:id="rId%d"/>`, xmlEscape(s.Name), i+1, i+1)
		}
		b.WriteString(`</sheets></workbook>`)
		if err := addFile(zw, "xl/workbook.xml", b.String()); err != nil {
			return err
		}
	}

	// xl/_rels/workbook.xml.rels
	{
		var b strings.Builder
		b.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`)
		fmt.Fprintf(&b, `<Relationships xmlns="%s">`, relNS)
		for i := range sheets {
			fmt.Fprintf(&b, `<Relationship Id="rId%d" Type="%s" Target="worksheets/sheet%d.xml"/>`, i+1, relSheet, i+1)
		}
		fmt.Fprintf(&b, `<Relationship Id="rId%d" Type="%s" Target="styles.xml"/>`, len(sheets)+1, relStyle)
		b.WriteString(`</Relationships>`)
		if err := addFile(zw, "xl/_rels/workbook.xml.rels", b.String()); err != nil {
			return err
		}
	}

	// xl/styles.xml (minimal)
	if err := addFile(zw, "xl/styles.xml", minimalStyles); err != nil {
		return err
	}

	// worksheets
	for i, s := range sheets {
		if err := addFile(zw, fmt.Sprintf("xl/worksheets/sheet%d.xml", i+1), sheetXML(s)); err != nil {
			return err
		}
	}

	if err := zw.Close(); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

func addFile(zw *zip.Writer, name, content string) error {
	w, err := zw.Create(name)
	if err != nil {
		return err
	}
	_, err = io.WriteString(w, content)
	return err
}

const minimalStyles = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
	`<styleSheet xmlns="` + mainNS + `">` +
	`<fonts count="1"><font><sz val="11"/><name val="Calibri"/></font></fonts>` +
	`<fills count="1"><fill><patternFill patternType="none"/></fill></fills>` +
	`<borders count="1"><border/></borders>` +
	`<cellStyleXfs count="1"><xf numFmtId="0" fontId="0" fillId="0" borderId="0"/></cellStyleXfs>` +
	`<cellXfs count="1"><xf numFmtId="0" fontId="0" fillId="0" borderId="0" xfId="0"/></cellXfs>` +
	`</styleSheet>`

func sheetXML(s Sheet) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`)
	fmt.Fprintf(&b, `<worksheet xmlns="%s">`, mainNS)
	if len(s.Widths) > 0 {
		b.WriteString(`<cols>`)
		for i, w := range s.Widths {
			fmt.Fprintf(&b, `<col min="%d" max="%d" width="%.1f" customWidth="1"/>`, i+1, i+1, w)
		}
		b.WriteString(`</cols>`)
	}
	b.WriteString(`<sheetData>`)

	rowNum := 0
	writeRow := func(cells []string) {
		rowNum++
		fmt.Fprintf(&b, `<row r="%d">`, rowNum)
		for i, v := range cells {
			if v == "" {
				continue
			}
			fmt.Fprintf(&b, `<c r="%s" t="inlineStr"><is><t>%s</t></is></c>`, cellRef(i, rowNum), xmlEscape(v))
		}
		b.WriteString(`</row>`)
	}

	writeRow(s.Header)
	for _, r := range s.Rows {
		writeRow(r)
	}

	b.WriteString(`</sheetData></worksheet>`)
	return b.String()
}

// cellRef returns the A1-style reference for a 0-based column and 1-based row.
func cellRef(col, row int) string {
	var s string
	col++
	for col > 0 {
		col--
		s = string(rune('A'+col%26)) + s
		col /= 26
	}
	return fmt.Sprintf("%s%d", s, row)
}

func xmlEscape(s string) string {
	r := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
		"'", "&apos;",
	)
	return r.Replace(s)
}

// WeekSheetNames returns the sorted sheet names for the given ISO weeks.
func WeekSheetName(year, week int) string {
	return fmt.Sprintf("Неделя %d (%d)", week, year)
}

// SortWeekKeys sorts (year, week) pairs ascending. Used by the exporter.
func SortWeekKeys(keys map[[2]int]bool) [][2]int {
	out := make([][2]int, 0, len(keys))
	for k := range keys {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i][0] != out[j][0] {
			return out[i][0] < out[j][0]
		}
		return out[i][1] < out[j][1]
	})
	return out
}
