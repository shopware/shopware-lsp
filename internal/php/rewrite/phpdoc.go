package phprewrite

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
)

type phpDocAnnotation struct {
	start     int
	end       int
	lineStart int
	lineEnd   int
}

type PHPDocAnnotation struct {
	Text  string
	Range cst.TextRange
}

// FindPHPDocAnnotation returns one complete standalone annotation owned by a
// declaration. Short names also match qualified annotations.
func FindPHPDocAnnotation(
	owner *phpsyntax.Node,
	annotationName string,
) (PHPDocAnnotation, bool) {
	token := leadingPHPDoc(owner)
	if token == nil {
		return PHPDocAnnotation{}, false
	}
	annotation, found := findPHPDocAnnotation(token.Text(), annotationName)
	if !found {
		return PHPDocAnnotation{}, false
	}
	rng := cst.TextRange{
		Start: token.Range().Start + uint32(annotation.start),
		End:   token.Range().Start + uint32(annotation.end),
	}
	return PHPDocAnnotation{
		Text:  token.Text()[annotation.start:annotation.end],
		Range: rng,
	}, true
}

// ReplacePHPDocAnnotation replaces one complete, standalone annotation while
// preserving the surrounding docblock. Nested parentheses and quoted strings
// are balanced before a rewrite is considered safe.
func (e *Editor) ReplacePHPDocAnnotation(
	owner *phpsyntax.Node,
	annotationName string,
	replacement string,
) (bool, error) {
	if e == nil || e.builder == nil {
		return false, fmt.Errorf("replace PHPDoc annotation: editor is nil")
	}
	token := leadingPHPDoc(owner)
	if token == nil {
		return false, nil
	}
	annotation, found := findPHPDocAnnotation(token.Text(), annotationName)
	if !found {
		return false, nil
	}
	replacement = strings.TrimSpace(replacement)
	if replacement == "" {
		return false, fmt.Errorf("replace PHPDoc annotation: replacement is empty")
	}
	if !strings.HasPrefix(replacement, "@") {
		replacement = "@" + replacement
	}
	return true, e.builder.ReplaceRange(cst.TextRange{
		Start: token.Range().Start + uint32(annotation.start),
		End:   token.Range().Start + uint32(annotation.end),
	}, replacement)
}

func (e *Editor) RemovePHPDocAnnotation(
	owner *phpsyntax.Node,
	annotationName string,
) (bool, error) {
	if e == nil || e.builder == nil {
		return false, fmt.Errorf("remove PHPDoc annotation: editor is nil")
	}
	token := leadingPHPDoc(owner)
	if token == nil {
		return false, nil
	}
	annotation, found := findPHPDocAnnotation(token.Text(), annotationName)
	if !found {
		return false, nil
	}
	return true, e.builder.ReplaceRange(cst.TextRange{
		Start: token.Range().Start + uint32(annotation.lineStart),
		End:   token.Range().Start + uint32(annotation.lineEnd),
	}, "")
}

func leadingPHPDoc(owner *phpsyntax.Node) *phpsyntax.Token {
	if owner == nil {
		return nil
	}
	var candidate *phpsyntax.Token
	for element := range owner.Descendants() {
		token, ok := element.(*phpsyntax.Token)
		if !ok {
			continue
		}
		if !token.Kind().IsTrivia() {
			break
		}
		if token.Kind() == phpsyntax.TkBlockComment && strings.HasPrefix(strings.TrimSpace(token.Text()), "/**") {
			candidate = token
		}
	}
	return candidate
}

func findPHPDocAnnotation(source, annotationName string) (phpDocAnnotation, bool) {
	annotationName = strings.TrimPrefix(strings.TrimSpace(annotationName), "@")
	annotationName = strings.TrimPrefix(annotationName, "\\")
	if annotationName == "" {
		return phpDocAnnotation{}, false
	}
	for start := 0; start < len(source); {
		end := strings.IndexByte(source[start:], '\n')
		if end < 0 {
			end = len(source)
		} else {
			end += start
		}
		line := source[start:end]
		prefix := 0
		for prefix < len(line) && (line[prefix] == ' ' || line[prefix] == '\t' || line[prefix] == '\r') {
			prefix++
		}
		if prefix < len(line) && line[prefix] == '*' {
			prefix++
			for prefix < len(line) && (line[prefix] == ' ' || line[prefix] == '\t') {
				prefix++
			}
			if prefix < len(line) && line[prefix] == '@' {
				nameStart := prefix + 1
				nameEnd := nameStart
				for nameEnd < len(line) && isPHPDocAnnotationNameCharacter(rune(line[nameEnd])) {
					nameEnd++
				}
				actualName := strings.TrimPrefix(line[nameStart:nameEnd], "\\")
				if phpDocAnnotationNamesEqual(actualName, annotationName) {
					return scanPHPDocAnnotation(source, start+prefix, start+nameEnd)
				}
			}
		}
		if end == len(source) {
			break
		}
		start = end + 1
	}
	return phpDocAnnotation{}, false
}

func phpDocAnnotationNamesEqual(actual, requested string) bool {
	if strings.EqualFold(actual, requested) {
		return true
	}
	if strings.Contains(requested, "\\") {
		return false
	}
	if separator := strings.LastIndex(actual, "\\"); separator >= 0 {
		return strings.EqualFold(actual[separator+1:], requested)
	}
	return false
}

func scanPHPDocAnnotation(source string, annotationStart, nameEnd int) (phpDocAnnotation, bool) {
	position := nameEnd
	for position < len(source) && (source[position] == ' ' || source[position] == '\t') {
		position++
	}
	annotationEnd := position
	if position < len(source) && source[position] == '(' {
		depth := 0
		quote := byte(0)
		escaped := false
		for ; position < len(source); position++ {
			character := source[position]
			if quote != 0 {
				if escaped {
					escaped = false
					continue
				}
				if character == '\\' {
					escaped = true
					continue
				}
				if character == quote {
					quote = 0
				}
				continue
			}
			if character == '\'' || character == '"' {
				quote = character
				continue
			}
			switch character {
			case '(':
				depth++
			case ')':
				depth--
				if depth == 0 {
					annotationEnd = position + 1
					position = len(source)
				}
			}
		}
		if depth != 0 || quote != 0 {
			return phpDocAnnotation{}, false
		}
	} else {
		annotationEnd = nameEnd
	}
	endOfLine := annotationEnd
	if newline := strings.IndexByte(source[endOfLine:], '\n'); newline >= 0 {
		endOfLine += newline
	} else {
		endOfLine = len(source)
	}
	if strings.TrimSpace(source[annotationEnd:endOfLine]) != "" {
		return phpDocAnnotation{}, false
	}
	startOfLine := strings.LastIndexByte(source[:annotationStart], '\n') + 1
	lineEnd := endOfLine
	if lineEnd < len(source) {
		lineEnd++
	}
	return phpDocAnnotation{
		start:     annotationStart,
		end:       annotationEnd,
		lineStart: startOfLine,
		lineEnd:   lineEnd,
	}, true
}

func isPHPDocAnnotationNameCharacter(character rune) bool {
	return character == '\\' || character == '_' || unicode.IsLetter(character) || unicode.IsDigit(character)
}
