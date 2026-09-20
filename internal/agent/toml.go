package agent

import (
	"fmt"
	"strings"
)

// tomlServerBlock renders Elephant's stdio server table for Codex.
func tomlServerBlock(command []string, actor string) string {
	args := make([]string, 0, len(command))
	for _, value := range command[1:] {
		args = append(args, tomlString(value))
	}
	return fmt.Sprintf("[mcp_servers.%s]\ncommand = %s\nargs = [%s]\n\n[mcp_servers.%s.env]\nELEPHANT_ACTOR_ID = %s\n",
		ServerName, tomlString(command[0]), strings.Join(args, ", "), ServerName, tomlString(actor))
}

// tomlSectionName returns the normalized table name of a header line.
func tomlSectionName(line string) (string, bool) {
	text := strings.TrimSpace(stripTOMLComment(line))
	if len(text) < 3 || text[0] != '[' || text[len(text)-1] != ']' || strings.HasPrefix(text, "[[") {
		return "", false
	}
	name := strings.NewReplacer("\"", "", "'", "", " ", "", "\t", "").Replace(strings.TrimSpace(text[1 : len(text)-1]))
	if name == "" {
		return "", false
	}
	return name, true
}

func ownsTOMLSection(name, section string) bool {
	return name == section || strings.HasPrefix(name, section+".")
}

// tomlBlock returns the raw text of a table and every subsection it owns.
func tomlBlock(content, section string) string {
	lines := strings.Split(content, "\n")
	start := -1
	for i, line := range lines {
		name, ok := tomlSectionName(line)
		if !ok {
			continue
		}
		if start >= 0 && ownsTOMLSection(name, section) {
			continue
		}
		if start >= 0 {
			return strings.Join(lines[start:i], "\n")
		}
		if ownsTOMLSection(name, section) {
			start = i
		}
	}
	if start < 0 {
		return ""
	}
	return strings.Join(lines[start:], "\n")
}

// setTOMLBlock replaces the table and its subsections, or appends them when
// absent. Every other line is preserved byte for byte.
func setTOMLBlock(content, section, block string) string {
	lines := strings.Split(strings.TrimRight(content, "\n"), "\n")
	if strings.TrimSpace(content) == "" {
		lines = nil
	}
	kept := make([]string, 0, len(lines))
	for i := 0; i < len(lines); {
		name, ok := tomlSectionName(lines[i])
		if ok && ownsTOMLSection(name, section) {
			i++
			for i < len(lines) {
				if _, ok := tomlSectionName(lines[i]); ok {
					break
				}
				i++
			}
			continue
		}
		kept = append(kept, lines[i])
		i++
	}
	for len(kept) > 0 && strings.TrimSpace(kept[len(kept)-1]) == "" {
		kept = kept[:len(kept)-1]
	}
	prefix := strings.Join(kept, "\n")
	if prefix != "" {
		prefix += "\n\n"
	}
	return prefix + block
}

// tomlValues reads scalar string values and raw array text from a table block.
func tomlValues(block string) map[string]string {
	values := map[string]string{}
	for _, line := range strings.Split(block, "\n") {
		text := strings.TrimSpace(stripTOMLComment(line))
		if text == "" || strings.HasPrefix(text, "[") {
			continue
		}
		equals := strings.IndexByte(text, '=')
		if equals <= 0 {
			continue
		}
		key := strings.Trim(strings.TrimSpace(text[:equals]), "\"'")
		value := strings.TrimSpace(text[equals+1:])
		if key == "" || value == "" {
			continue
		}
		if strings.HasPrefix(value, "[") {
			values[key] = value
			continue
		}
		if scalar, ok := tomlScalar(value); ok {
			values[key] = scalar
		}
	}
	return values
}

// tomlArray decodes a bounded array of string values.
func tomlArray(raw string) []string {
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(raw, "[") || !strings.HasSuffix(raw, "]") {
		return nil
	}
	var out []string
	for _, part := range splitTOMLArray(raw[1 : len(raw)-1]) {
		if value, ok := tomlScalar(strings.TrimSpace(part)); ok {
			out = append(out, value)
		}
	}
	return out
}

func splitTOMLArray(inner string) []string {
	var parts []string
	var current strings.Builder
	quote := rune(0)
	escaped := false
	for _, r := range inner {
		switch {
		case escaped:
			escaped = false
			current.WriteRune(r)
		case quote == '"' && r == '\\':
			escaped = true
			current.WriteRune(r)
		case quote != 0:
			current.WriteRune(r)
			if r == quote {
				quote = 0
			}
		case r == '"' || r == '\'':
			quote = r
			current.WriteRune(r)
		case r == ',':
			parts = append(parts, current.String())
			current.Reset()
		default:
			current.WriteRune(r)
		}
	}
	if strings.TrimSpace(current.String()) != "" {
		parts = append(parts, current.String())
	}
	return parts
}
