package containment

import "strings"

// segments splits a command into the pieces a shell would run as separate
// commands, each a list of tokens with its redirection operators kept in place.
//
// It is a reader, not a shell. It has to be right about the shapes that decide
// containment — where a word ends, which operator separates two commands, and
// which text is data rather than a command — and it does not have to execute
// anything. Where it cannot be sure, the caller's stance is to refuse, so an
// unparsed oddity becomes a refusal rather than a pass.
func segments(command string) []cmdseg {
	var out []cmdseg
	current := []string{}
	starts := true
	flush := func(next bool) {
		if len(current) > 0 {
			out = append(out, cmdseg{words: current, startsPipeline: starts})
			current = []string{}
		}
		starts = next
	}
	for _, tok := range tokenize(stripHeredocBodies(command)) {
		switch tok {
		// A pipeline is ONE unit for containment. Its stages share data, so a
		// path named in an early stage can be what a later stage writes to
		// without ever appearing beside it — `grep -rl x ../other | xargs sed
		// -i` names the other checkout nowhere near the command that edits it.
		case "|":
			flush(false)
		case "&&", "||", ";", "&", "\n":
			flush(true)
		default:
			current = append(current, tok)
		}
	}
	flush(true)
	return out
}

// cmdseg is one command, and whether it opens a pipeline or continues one.
type cmdseg struct {
	words          []string
	startsPipeline bool
}

// stripHeredocBodies removes here-document contents.
//
// A heredoc body is DATA. This matters more than it sounds: the bodies in this
// repository's own commands contain prose about `rm -rf`, paths in other
// checkouts, and whole Go and Python programs. Parsed as commands they would
// produce refusals for text that runs nothing, and a check that fires on a
// comment is one people route around.
//
// The delimiter is taken as written and matched against the whole line, which is
// what a shell does for the unindented form. `<<-` allows leading tabs, so those
// are trimmed before comparing.
func stripHeredocBodies(command string) string {
	lines := strings.Split(command, "\n")
	var out []string
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		out = append(out, line)
		delims := heredocDelimiters(line)
		if len(delims) == 0 {
			continue
		}
		// Consume the bodies that follow, in the order the operators appeared.
		for _, d := range delims {
			for i+1 < len(lines) {
				i++
				if strings.TrimLeft(lines[i], "\t") == d.word || lines[i] == d.word {
					break
				}
			}
		}
	}
	return strings.Join(out, "\n")
}

type heredoc struct{ word string }

// heredocDelimiters finds the `<<WORD` operators on one line, ignoring any
// inside quotes.
func heredocDelimiters(line string) []heredoc {
	var out []heredoc
	var quote rune
	runes := []rune(line)
	for i := 0; i < len(runes); i++ {
		c := runes[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			}
			continue
		case c == '\'' || c == '"':
			quote = c
			continue
		case c == '\\':
			i++
			continue
		case c == '<' && i+1 < len(runes) && runes[i+1] == '<':
			i += 2
			if i < len(runes) && runes[i] == '-' {
				i++
			}
			for i < len(runes) && (runes[i] == ' ' || runes[i] == '\t') {
				i++
			}
			// The delimiter may be quoted; the quotes are not part of it.
			var word strings.Builder
			var wq rune
			for i < len(runes) {
				ch := runes[i]
				if wq != 0 {
					if ch == wq {
						wq = 0
						i++
						continue
					}
					word.WriteRune(ch)
					i++
					continue
				}
				if ch == '\'' || ch == '"' {
					wq = ch
					i++
					continue
				}
				if ch == ' ' || ch == '\t' || ch == ';' || ch == '|' || ch == '&' {
					break
				}
				word.WriteRune(ch)
				i++
			}
			if w := word.String(); w != "" {
				out = append(out, heredoc{word: w})
			}
			i--
		}
	}
	return out
}

// tokenize splits text into words and operators, honouring quotes and
// backslash escapes. Quotes are removed from the word they surround, because a
// path is the same path however it was quoted.
func tokenize(text string) []string {
	var out []string
	var cur strings.Builder
	var quote rune
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, cur.String())
			cur.Reset()
		}
	}

	runes := []rune(text)
	for i := 0; i < len(runes); i++ {
		c := runes[i]

		if quote != 0 {
			if c == quote {
				quote = 0
				continue
			}
			if c == '\\' && quote == '"' && i+1 < len(runes) {
				i++
				cur.WriteRune(runes[i])
				continue
			}
			cur.WriteRune(c)
			continue
		}

		switch {
		case c == '\'' || c == '"':
			quote = c
		case c == '\\':
			if i+1 < len(runes) {
				i++
				if runes[i] == '\n' {
					continue // a line continuation joins the words around it
				}
				cur.WriteRune(runes[i])
			}
		case c == '\n':
			flush()
			out = append(out, "\n")
		case c == ' ' || c == '\t' || c == '\r':
			flush()
		case c == '#' && cur.Len() == 0:
			// A comment runs to the end of the line and is not a command.
			for i < len(runes) && runes[i] != '\n' {
				i++
			}
			flush()
			out = append(out, "\n")
		default:
			if op, width := operatorAt(runes, i); op != "" {
				flush()
				out = append(out, op)
				i += width - 1
				continue
			}
			cur.WriteRune(c)
		}
	}
	flush()
	return out
}

// operators are matched longest-first so `>>` is never read as two `>`.
var operators = []string{"&>>", "<<-", "&&", "||", ">>", "<<", "&>", ">|", ";;", ";", "|", "&", ">", "<"}

// operatorAt reports the operator starting at i, and how many runes it spans.
// A file-descriptor prefix (`2>`, `1>>`) is part of the operator.
func operatorAt(runes []rune, i int) (string, int) {
	// A single leading digit before > or >> is a file descriptor.
	if runes[i] >= '0' && runes[i] <= '9' && i+1 < len(runes) && runes[i+1] == '>' {
		if i+2 < len(runes) && runes[i+2] == '>' {
			return string(runes[i : i+3]), 3
		}
		return string(runes[i : i+2]), 2
	}
	rest := string(runes[i:])
	for _, op := range operators {
		if strings.HasPrefix(rest, op) {
			if op == "<<-" || op == "<<" {
				return "<<", len(op)
			}
			return op, len(op)
		}
	}
	return "", 0
}
