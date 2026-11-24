package parser

import (
	"encoding/json"
	"errors"
	"strings"
)

// RepairJSON coerces sloppy model JSON into valid JSON (SPEC §13.4):
// markdown fences, single-quoted strings, unquoted keys, Python literals,
// trailing commas and unbalanced braces. Returns the original bytes when
// already valid. Errors only when repair fails.
func RepairJSON(raw string) ([]byte, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return []byte("{}"), nil
	}
	if json.Valid([]byte(s)) {
		return []byte(s), nil
	}
	s = stripFence(s)
	s = replaceSmartQuotes(s)
	s = repairStateful(s)
	if json.Valid([]byte(s)) {
		return []byte(s), nil
	}
	s = balance(s)
	if json.Valid([]byte(s)) {
		return []byte(s), nil
	}
	return nil, errors.New("invalid JSON after repair: " + truncate(s, 200))
}

// stripFence removes surrounding markdown code fences, preferring the
// content after a ```json marker.
func stripFence(s string) string {
	if !strings.Contains(s, "```") {
		return s
	}
	s = strings.Replace(s, "```json", "```", 1)
	parts := strings.Split(s, "```")
	if len(parts) >= 2 {
		inner := parts[1] // text between the first pair of fences
		if strings.TrimSpace(inner) != "" {
			return strings.TrimSpace(inner)
		}
	}
	return strings.ReplaceAll(s, "```", "")
}

func replaceSmartQuotes(s string) string {
	r := strings.NewReplacer(
		"“", `"`, "”", `"`, // “ ”
		"‘", "'", "’", "'", // ‘ ’
		"«", `"`, "»", `"`,
	)
	return r.Replace(s)
}

// repairStateful walks the text once outside strings, fixing:
// single-quoted strings, unquoted object keys, Python literals, trailing commas.
func repairStateful(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 16)
	rs := []byte(s)
	inStr := false
	var strQuote byte
	prevSig := byte('{') // last significant char outside strings ("outside" view)

	for i := 0; i < len(rs); i++ {
		c := rs[i]
		if inStr {
			if c == '\\' && i+1 < len(rs) { // copy escape pair verbatim
				if strQuote == '\'' && rs[i+1] == '"' { // escape inner " when re-quoting
					b.WriteString(`\"`)
					i++
					continue
				}
				b.WriteByte(c)
				i++
				b.WriteByte(rs[i])
				continue
			}
			if c == '"' && strQuote == '\'' { // must escape inside double quotes
				b.WriteString(`\"`)
				continue
			}
			if c == strQuote {
				b.WriteByte('"') // closes both '…' and "…" forms
				inStr = false
				prevSig = '"'
				continue
			}
			b.WriteByte(c)
			continue
		}
		switch {
		case c == '"':
			inStr = true
			strQuote = '"'
			b.WriteByte(c)
		case c == '\'':
			inStr = true
			strQuote = '\''
			b.WriteByte('"') // re-open as double-quoted
		case isIdentStart(c) && (prevSig == '{' || prevSig == ',' || prevSig == ':' || prevSig == '['):
			j := i
			for j < len(rs) && isIdentByte(rs[j]) {
				j++
			}
			word := rs[i:j]
			k := j
			for k < len(rs) && isSpace(rs[k]) {
				k++
			}
			if k < len(rs) && rs[k] == ':' { // unquoted key
				b.WriteByte('"')
				b.Write(word)
				b.WriteByte('"')
				i = j - 1
				prevSig = '"'
				continue
			}
			if lit, ok := pythonLiteral(string(word)); ok {
				b.WriteString(lit)
				i = j - 1
				prevSig = 'x'
				continue
			}
			b.Write(word) // bare word (invalid, but keep it for balance/valid check)
			i = j - 1
			prevSig = 'x'
		case c == ',':
			k := i + 1
			for k < len(rs) && isSpace(rs[k]) {
				k++
			}
			if k < len(rs) && (rs[k] == '}' || rs[k] == ']') {
				continue // drop trailing comma
			}
			b.WriteByte(c)
			prevSig = ','
		default:
			b.WriteByte(c)
			if !isSpace(c) {
				prevSig = c
			}
		}
	}
	return b.String()
}

// balance appends missing closers for unterminated { or [ (outside strings).
func balance(s string) string {
	var stack []byte
	inStr := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inStr {
			if c == '\\' {
				i++
				continue
			}
			if c == '"' {
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
		case '{', '[':
			stack = append(stack, c)
		case '}', ']':
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		}
	}
	if len(stack) == 0 {
		return s
	}
	var b strings.Builder
	b.WriteString(s)
	for i := len(stack) - 1; i >= 0; i-- {
		if stack[i] == '{' {
			b.WriteString("}")
		} else {
			b.WriteString("]")
		}
	}
	return b.String()
}

func pythonLiteral(word string) (string, bool) {
	switch word {
	case "True":
		return "true", true
	case "False":
		return "false", true
	case "None", "null", "NULL":
		return "null", true
	case "NaN", "nan":
		return "0", true
	}
	return "", false
}

func isIdentStart(c byte) bool {
	return c == '_' || c == '$' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}
func isIdentByte(c byte) bool { return isIdentStart(c) || (c >= '0' && c <= '9') }
func isSpace(c byte) bool     { return c == ' ' || c == '\t' || c == '\n' || c == '\r' }

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
