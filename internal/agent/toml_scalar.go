package agent

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// tomlScalar decodes a single-line TOML string literal.
func tomlScalar(value string) (string, bool) {
	if len(value) < 2 {
		return "", false
	}
	if value[0] == '\'' {
		end := strings.IndexByte(value[1:], '\'')
		if end < 0 {
			return "", false
		}
		return value[1 : 1+end], true
	}
	if value[0] != '"' {
		return "", false
	}
	var out strings.Builder
	escaped := false
	for i := 1; i < len(value); i++ {
		c := value[i]
		if escaped {
			escaped = false
			switch c {
			case 'n':
				out.WriteByte('\n')
			case 't':
				out.WriteByte('\t')
			case 'r':
				out.WriteByte('\r')
			case 'b':
				out.WriteByte('\b')
			case 'f':
				out.WriteByte('\f')
			case '"', '\\':
				out.WriteByte(c)
			case 'u', 'U':
				width := 4
				if c == 'U' {
					width = 8
				}
				if i+width >= len(value) {
					return "", false
				}
				var decoded rune
				if _, err := fmt.Sscanf(value[i+1:i+1+width], "%x", &decoded); err != nil {
					return "", false
				}
				out.WriteRune(decoded)
				i += width
			default:
				return "", false
			}
			continue
		}
		switch c {
		case '\\':
			escaped = true
		case '"':
			return out.String(), true
		default:
			out.WriteByte(c)
		}
	}
	return "", false
}

// tomlString encodes a basic TOML string.
func tomlString(value string) string {
	var out strings.Builder
	out.WriteByte('"')
	for _, r := range value {
		switch r {
		case '\\':
			out.WriteString(`\\`)
		case '"':
			out.WriteString(`\"`)
		case '\n':
			out.WriteString(`\n`)
		case '\r':
			out.WriteString(`\r`)
		case '\t':
			out.WriteString(`\t`)
		case '\b':
			out.WriteString(`\b`)
		case '\f':
			out.WriteString(`\f`)
		default:
			if r < 0x20 || r == 0x7f || r == utf8.RuneError {
				fmt.Fprintf(&out, `\u%04X`, r)
				continue
			}
			out.WriteRune(r)
		}
	}
	out.WriteByte('"')
	return out.String()
}

// stripTOMLComment removes a trailing comment that is outside a string.
func stripTOMLComment(line string) string {
	quote := byte(0)
	escaped := false
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case escaped:
			escaped = false
		case quote == '"' && c == '\\':
			escaped = true
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '#':
			return line[:i]
		}
	}
	return line
}
