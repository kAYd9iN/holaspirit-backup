package main

import "regexp"

// controlChars matches ASCII control characters and ANSI escape sequences.
// API-supplied or operator-supplied strings are passed through sanitizeLog
// before logging to prevent log injection (issue #18) — a value containing
// "\n" or terminal escapes cannot forge additional log lines.
var controlChars = regexp.MustCompile(`[\x00-\x1f\x7f]|\x1b\[[0-9;]*[a-zA-Z]`)

func sanitizeLog(s string) string {
	return controlChars.ReplaceAllString(s, "?")
}
