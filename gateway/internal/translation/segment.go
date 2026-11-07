package translation

import (
	"encoding/json"
	"strings"
)

// segmentBytes bounds one worker request. The 8B model takes about 16 s per KB,
// so a long reply sent whole exceeded the inference deadline, was killed and
// retried forever while every other translation waited behind it. Segments are
// cached separately, and user requests can run between them.
const segmentBytes = 2000

// segments splits text at line boundaries outside fenced code, preferring blank
// lines. Concatenating the result restores text exactly. A unit longer than the
// limit (one long line or code block) stays whole: the worker pieces prose and
// passes code through.
func segments(text string) []string {
	if len(text) <= segmentBytes {
		return []string{text}
	}
	// The worker returns JSON documents unchanged only when it sees all of one.
	if trimmed := strings.TrimSpace(text); (strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[")) && json.Valid([]byte(trimmed)) {
		return []string{text}
	}
	var out []string
	start, blank, fence := 0, -1, ""
	for i := 0; i < len(text); {
		end := strings.IndexByte(text[i:], '\n')
		if end < 0 {
			end = len(text)
		} else {
			end += i + 1
		}
		line := text[i:end]
		marker := fenceMarker(line)
		inside := fence != ""
		switch {
		case fence == "" && marker != "":
			fence = marker
		case fence != "" && strings.HasPrefix(strings.TrimLeft(line, " \t"), fence):
			fence = ""
		}
		// Cut before this line when it would overflow, never inside a fence.
		blankLine := strings.TrimSpace(line) == ""
		// Blank lines stay with the paragraph before them.
		if !inside && !blankLine && i > start && end-start > segmentBytes {
			cut := i
			if blank > start+segmentBytes/2 {
				cut = blank
			}
			out = append(out, text[start:cut])
			start, blank = cut, -1
		}
		if !inside && blankLine {
			blank = end
		}
		i = end
	}
	if start < len(text) {
		out = append(out, text[start:])
	}
	return out
}

// fenceMarker mirrors worker.py: a fence opens with three or more backticks or
// tildes after optional indentation and closes at a line starting with the same run.
func fenceMarker(line string) string {
	trimmed := strings.TrimLeft(line, " \t")
	if trimmed == "" || (trimmed[0] != '`' && trimmed[0] != '~') {
		return ""
	}
	n := 0
	for n < len(trimmed) && trimmed[n] == trimmed[0] {
		n++
	}
	if n < 3 {
		return ""
	}
	return trimmed[:n]
}
