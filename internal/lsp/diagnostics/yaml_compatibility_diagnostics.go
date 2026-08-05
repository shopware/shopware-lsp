package diagnostics

import (
	"context"
	"path/filepath"
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	yamlquery "github.com/shopware/shopware-lsp/internal/parser/yaml/query"
	yamlsyntax "github.com/shopware/shopware-lsp/internal/parser/yaml/syntax"
	"github.com/shopware/shopware-lsp/internal/php/project"
)

const (
	yamlInvalidQuotedEscapeCode lsp.DiagnosticID = "symfony.yaml.quoted_escape"
	yamlUnquotedIndicatorCode   lsp.DiagnosticID = "symfony.yaml.unquoted_indicator"
	yamlUnquotedColonCode       lsp.DiagnosticID = "symfony.yaml.unquoted_colon"

	yamlInvalidQuotedEscapeMessage = "Not escaping a backslash in a double-quoted string is deprecated"
	yamlUnquotedColonMessage       = "Using a colon in the unquoted mapping value is deprecated since Symfony 2.8 and will throw a ParseException in 3.0"
)

// YAMLCompatibilityAnalyzer ports the Symfony YAML deprecation
// inspections against the native, lossless YAML CST.
type YAMLCompatibilityAnalyzer struct {
	project *project.Model
}

func NewYAMLCompatibilityAnalyzer(
	model *project.Model,
) *YAMLCompatibilityAnalyzer {
	return &YAMLCompatibilityAnalyzer{project: model}
}

func (p *YAMLCompatibilityAnalyzer) Analyze(
	ctx context.Context,
	document *lsp.TextDocument,
) ([]lsp.Problem, error) {
	if p == nil || document == nil || document.SyntaxTree == nil ||
		document.SyntaxTree.Root == nil ||
		!isYAMLDocument(document.URI) ||
		!p.supportsSymfonyYAMLDeprecations() {
		return nil, nil
	}

	var result []lsp.Problem
	for element := range document.SyntaxTree.Root.Descendants() {
		if ctx.Err() != nil {
			return result, nil
		}
		token, ok := element.(*cst.Token)
		if !ok {
			continue
		}
		switch token.Kind() {
		case yamlsyntax.TkDoubleQuotedScalar:
			replacement, found := escapedDoubleQuotedYAMLScalar(token.Text())
			if !found {
				continue
			}
			result = append(result, yamlCompatibilityDiagnostic(
				document,
				token.Range(),
				yamlInvalidQuotedEscapeCode,
				yamlInvalidQuotedEscapeMessage,
				replacement,
			))
		case yamlsyntax.TkPlainScalar:
			rng, replacement, indicator, found :=
				quotedLeadingIndicatorYAMLScalar(token)
			if !found {
				continue
			}
			message := "Deprecated usage of '" + string(indicator) +
				"' at the beginning of unquoted string"
			if indicator == '%' {
				message = "Not quoting a scalar starting with the '%' indicator character is deprecated since Symfony 3.1"
			}
			result = append(result, yamlCompatibilityDiagnostic(
				document,
				rng,
				yamlUnquotedIndicatorCode,
				message,
				replacement,
			))
		}
	}
	result = append(
		result,
		unquotedColonYAMLDiagnostics(ctx, document)...,
	)
	sort.SliceStable(result, func(left, right int) bool {
		leftRange := result[left].Range
		rightRange := result[right].Range
		return leftRange.Start < rightRange.Start
	})
	return result, nil
}

func (p *YAMLCompatibilityAnalyzer) supportsSymfonyYAMLDeprecations() bool {
	if p == nil || p.project == nil {
		return false
	}
	version, found := p.project.DependencyVersion(
		"symfony/http-kernel",
		"symfony/framework-bundle",
		"symfony/yaml",
		"symfony/symfony",
	)
	return found && version.AtLeast(2, 8)
}

func isYAMLDocument(uri string) bool {
	switch strings.ToLower(filepath.Ext(uri)) {
	case ".yaml", ".yml":
		return true
	default:
		return false
	}
}

func escapedDoubleQuotedYAMLScalar(text string) (string, bool) {
	if len(text) < 2 || text[0] != '"' || text[len(text)-1] != '"' {
		return "", false
	}
	// Preserve the reference inspection's guard against scanning very long
	// strings on every editor update.
	if len(text)-2 >= 255 {
		return "", false
	}

	var replacement strings.Builder
	replacement.Grow(len(text) + 4)
	replacement.WriteByte('"')
	invalid := false
	for position := 1; position < len(text)-1; position++ {
		value := text[position]
		if value != '\\' {
			replacement.WriteByte(value)
			continue
		}

		nextPosition := position + 1
		if nextPosition >= len(text)-1 {
			return "", false
		}
		next := text[nextPosition]
		if validYAMLDoubleQuotedEscapeIndicator(next) {
			replacement.WriteByte(value)
			replacement.WriteByte(next)
			position++
			continue
		}

		invalid = true
		replacement.WriteString(`\\`)
	}
	replacement.WriteByte('"')
	return replacement.String(), invalid
}

func validYAMLDoubleQuotedEscapeIndicator(value byte) bool {
	switch value {
	case '0', 'a', 'b', 't', 'n', 'v', 'f', 'r', 'e', ' ', '"', '/',
		'\\', 'N', '_', 'L', 'P', 'x', 'u', 'U', '\r', '\n':
		return true
	default:
		return false
	}
}

func quotedLeadingIndicatorYAMLScalar(
	token *cst.Token,
) (cst.TextRange, string, byte, bool) {
	if token == nil {
		return cst.TextRange{}, "", 0, false
	}
	text := strings.TrimRight(token.Text(), " \t")
	if len(text) <= 1 {
		return cst.TextRange{}, "", 0, false
	}
	indicator := text[0]
	switch indicator {
	case '@', '`', '|', '>', '%':
	default:
		return cst.TextRange{}, "", 0, false
	}
	rng := token.Range()
	rng.End = rng.Start + uint32(len(text))
	return rng, yamlSingleQuotedScalar(text), indicator, true
}

func unquotedColonYAMLDiagnostics(
	ctx context.Context,
	document *lsp.TextDocument,
) []lsp.Problem {
	pairs := yamlquery.Nodes(
		document.SyntaxTree.Root,
		yamlsyntax.YamlPair,
	)
	var result []lsp.Problem
	for _, recoveredPair := range pairs {
		if ctx.Err() != nil {
			return result
		}
		colon := firstSignificantYAMLToken(recoveredPair)
		if colon == nil || colon.Kind() != yamlsyntax.TkColon ||
			colon.Range().Start != recoveredPair.Range().Start ||
			int(colon.Range().End) >= len(document.Source) ||
			document.Source[colon.Range().End] != ' ' {
			continue
		}

		previousValue := boundaryLinkedPlainYAMLValue(
			pairs,
			recoveredPair,
		)
		if previousValue == nil {
			continue
		}
		last := lastSignificantYAMLToken(recoveredPair)
		if last == nil {
			continue
		}
		start := previousValue.Range().Start
		end := last.Range().End
		for end > start {
			switch document.Source[end-1] {
			case ' ', '\t':
				end--
			default:
				goto trimmed
			}
		}
	trimmed:
		if end <= start || end-start > 200 {
			continue
		}
		text := document.Source[start:end]
		if strings.ContainsAny(text, "\r\n") ||
			hasIndentedYAMLContinuation(
				document.Source,
				recoveredPair.Range().End,
				start,
			) {
			continue
		}

		rng := cst.TextRange{Start: start, End: end}
		result = append(result, yamlCompatibilityDiagnostic(
			document,
			rng,
			yamlUnquotedColonCode,
			yamlUnquotedColonMessage,
			yamlSingleQuotedScalar(text),
		))
	}
	return result
}

func boundaryLinkedPlainYAMLValue(
	pairs []*yamlsyntax.Node,
	recoveredPair *yamlsyntax.Node,
) *yamlsyntax.Token {
	if recoveredPair == nil {
		return nil
	}
	var result *yamlsyntax.Token
	var resultStart uint32
	for _, pair := range pairs {
		if pair == nil || pair == recoveredPair ||
			pair.Range().End != recoveredPair.Range().Start {
			continue
		}
		value := yamlquery.PairValue(pair)
		if value == nil || value.Kind() != yamlsyntax.YamlScalar {
			continue
		}
		token := value.ChildTokenOfKind(yamlsyntax.TkPlainScalar)
		if token == nil || token.Range().End != recoveredPair.Range().Start {
			continue
		}
		if result == nil || pair.Range().Start >= resultStart {
			result = token
			resultStart = pair.Range().Start
		}
	}
	return result
}

func firstSignificantYAMLToken(node *yamlsyntax.Node) *yamlsyntax.Token {
	if node == nil {
		return nil
	}
	for element := range node.Descendants() {
		token, ok := element.(*yamlsyntax.Token)
		if ok && isSignificantYAMLToken(token) {
			return token
		}
	}
	return nil
}

func lastSignificantYAMLToken(node *yamlsyntax.Node) *yamlsyntax.Token {
	if node == nil {
		return nil
	}
	var result *yamlsyntax.Token
	for element := range node.Descendants() {
		token, ok := element.(*yamlsyntax.Token)
		if ok && isSignificantYAMLToken(token) {
			result = token
		}
	}
	return result
}

func isSignificantYAMLToken(token *yamlsyntax.Token) bool {
	if token == nil {
		return false
	}
	switch token.Kind() {
	case yamlsyntax.TkWhitespace,
		yamlsyntax.TkComment,
		yamlsyntax.TkIndent,
		yamlsyntax.TkLineBreak:
		return false
	default:
		return true
	}
}

func hasIndentedYAMLContinuation(
	source string,
	after,
	valueStart uint32,
) bool {
	next := int(after)
	if next > len(source) {
		return false
	}
	for next > 0 && next <= len(source) &&
		source[next-1] != '\n' && source[next-1] != '\r' {
		if next == len(source) {
			return false
		}
		next++
	}
	if next >= len(source) {
		return false
	}

	baseLineStart := strings.LastIndexAny(
		source[:valueStart],
		"\r\n",
	) + 1
	baseIndent := yamlIndentWidth(source[baseLineStart:int(valueStart)])
	lineEnd := next
	for lineEnd < len(source) &&
		source[lineEnd] != '\r' && source[lineEnd] != '\n' {
		lineEnd++
	}
	nextLine := source[next:lineEnd]
	trimmed := strings.TrimLeft(nextLine, " \t")
	if trimmed == "" || strings.HasPrefix(trimmed, "#") {
		return false
	}
	return yamlIndentWidth(nextLine) > baseIndent
}

func yamlIndentWidth(text string) int {
	width := 0
	for _, value := range text {
		switch value {
		case ' ':
			width++
		case '\t':
			width += 2
		default:
			return width
		}
	}
	return width
}

func yamlSingleQuotedScalar(text string) string {
	return "'" + strings.ReplaceAll(text, "'", "''") + "'"
}

func yamlCompatibilityDiagnostic(
	_ *lsp.TextDocument,
	rng cst.TextRange,
	code lsp.DiagnosticID,
	message,
	replacement string,
) lsp.Problem {
	return lsp.Problem{
		Range:    rng,
		Message:  message,
		Severity: protocol.DiagnosticSeverityHint,
		Source:   "symfony",
		ID:       code,
		Tags: []protocol.DiagnosticTag{
			protocol.DiagnosticTagDeprecated,
		},
		Payload: map[string]any{"replacement": replacement},
	}
}
