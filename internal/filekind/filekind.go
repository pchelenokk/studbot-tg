// Package filekind validates uploaded attachments: which extensions are
// accepted, which may be served inline, and whether the file content really
// matches the extension.
package filekind

import (
	"bytes"
	"fmt"
	"io"
	"path/filepath"
	"strings"
)

var documents = map[string]bool{
	".pdf": true, ".doc": true, ".docx": true, ".xls": true, ".xlsx": true,
	".ppt": true, ".pptx": true, ".txt": true, ".rtf": true, ".odt": true,
	".ods": true, ".odp": true, ".djvu": true, ".zip": true, ".fb2": true,
}

var images = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".gif": true, ".webp": true,
}

// inlineSafe lists the formats the browser may render in place. Everything
// else is sent as an attachment, so an uploaded file can never run as script
// in the application origin.
var inlineSafe = map[string]bool{
	".pdf": true, ".jpg": true, ".jpeg": true, ".png": true, ".gif": true, ".webp": true,
}

// Allowed reports whether the extension is accepted at all.
func Allowed(ext string) bool {
	ext = strings.ToLower(ext)
	return documents[ext] || images[ext]
}

// Ext returns the lowercased, whitelisted extension of the given file name.
func Ext(name string) (string, error) {
	ext := strings.ToLower(filepath.Ext(name))
	if ext == "" {
		return "", fmt.Errorf("у файла нет расширения")
	}
	if !Allowed(ext) {
		return "", fmt.Errorf("недопустимый тип файла: %s", ext)
	}
	return ext, nil
}

// InlineSafe reports whether the extension may be served without a
// Content-Disposition: attachment header.
func InlineSafe(ext string) bool { return inlineSafe[strings.ToLower(ext)] }

// SniffHeader checks the first bytes of a file against its extension. Only the
// formats that may be served inline are verified; other formats are forced to
// download and are not inspected. An empty header is accepted for zero-byte
// files only when the extension carries no signature.
func SniffHeader(head []byte, ext string) error {
	ext = strings.ToLower(ext)
	switch ext {
	case ".pdf":
		if !bytes.HasPrefix(head, []byte("%PDF")) {
			return fmt.Errorf("файл не похож на PDF")
		}
	case ".jpg", ".jpeg":
		if !bytes.HasPrefix(head, []byte{0xFF, 0xD8, 0xFF}) {
			return fmt.Errorf("файл не похож на JPEG")
		}
	case ".png":
		if !bytes.HasPrefix(head, []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}) {
			return fmt.Errorf("файл не похож на PNG")
		}
	case ".gif":
		if !bytes.HasPrefix(head, []byte("GIF87a")) && !bytes.HasPrefix(head, []byte("GIF89a")) {
			return fmt.Errorf("файл не похож на GIF")
		}
	case ".webp":
		if len(head) < 12 || !bytes.HasPrefix(head, []byte("RIFF")) || !bytes.Equal(head[8:12], []byte("WEBP")) {
			return fmt.Errorf("файл не похож на WebP")
		}
	}
	return nil
}

// Sniff reads the file header, validates it and rewinds the reader.
func Sniff(r io.ReadSeeker, ext string) error {
	head := make([]byte, 12)
	n, err := io.ReadFull(r, head)
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return err
	}
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		return err
	}
	return SniffHeader(head[:n], ext)
}

// Documents and Images expose the accepted extensions (used in messages).
func Strings() []string {
	out := make([]string, 0, len(documents)+len(images))
	for ext := range documents {
		out = append(out, ext)
	}
	for ext := range images {
		out = append(out, ext)
	}
	return out
}
