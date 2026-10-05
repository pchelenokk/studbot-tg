package bot

import (
	"strings"
	"unicode/utf8"
)

// htmlReplacer escapes user supplied text before it is inserted into messages
// sent with Telegram's HTML parse mode.
var htmlReplacer = strings.NewReplacer(
	"&", "&amp;",
	"<", "&lt;",
	">", "&gt;",
	`"`, "&quot;",
)

// escHTML makes s safe for ParseMode "HTML".
func escHTML(s string) string { return htmlReplacer.Replace(s) }

// fit shortens s to at most n runes, appending an ellipsis when it was cut.
// It works on runes, so multi-byte text is never cut in the middle.
func fit(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return truncate(s, n) + "…"
}
