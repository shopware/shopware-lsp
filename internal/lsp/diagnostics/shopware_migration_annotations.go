package diagnostics

import (
	"context"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	phprewrite "github.com/shopware/shopware-lsp/internal/php/rewrite"
)

const routeAnnotationDefaultsCode lsp.DiagnosticID = "shopware.migration.annotation.route_defaults"

func (p *ShopwareMigrationAnalyzer) routeAnnotationMigrationProblems(
	ctx context.Context,
	root *phpsyntax.Node,
) []lsp.Problem {
	var result []lsp.Problem
	for _, class := range phpquery.Classes(root) {
		if ctx.Err() != nil {
			return result
		}
		result = append(result, routeAnnotationProblemsForOwner(class)...)
		for _, method := range phpquery.Methods(class) {
			result = append(result, routeAnnotationProblemsForOwner(method)...)
		}
	}
	return result
}

func routeAnnotationProblemsForOwner(owner *phpsyntax.Node) []lsp.Problem {
	route, hasRoute := phprewrite.FindPHPDocAnnotation(owner, "Route")
	var result []lsp.Problem
	for _, migration := range []struct {
		annotation string
		kind       string
		key        string
	}{
		{"Captcha", "captcha", "_captcha"},
		{"LoginRequired", "login-required", "_loginRequired"},
	} {
		tag, found := phprewrite.FindPHPDocAnnotation(owner, migration.annotation)
		if !found || !hasRoute {
			continue
		}
		replacement, changed, safe := addDoctrineRouteDefault(
			route.Text,
			migration.key,
			"true",
		)
		if !changed {
			continue
		}
		result = append(result, routeAnnotationProblem(
			owner,
			tag,
			migration.kind,
			migration.annotation,
			replacement,
			safe,
		))
	}
	routeScope, found := phprewrite.FindPHPDocAnnotation(owner, "RouteScope")
	if !found {
		return result
	}
	scopes, scopesOK := doctrineAnnotationNamedValue(routeScope.Text, "scopes")
	if !scopesOK || !strings.HasPrefix(strings.TrimSpace(scopes), "{") {
		result = append(result, routeAnnotationProblem(
			owner,
			routeScope,
			"route-scope",
			"RouteScope",
			"",
			false,
		))
		return result
	}
	replacement := ""
	changed := true
	safe := true
	if hasRoute {
		replacement, changed, safe = addDoctrineRouteDefault(
			route.Text,
			"_routeScope",
			strings.TrimSpace(scopes),
		)
	} else {
		replacement = `@Route(defaults={"_routeScope"=` + strings.TrimSpace(scopes) + `})`
	}
	if changed {
		result = append(result, routeAnnotationProblem(
			owner,
			routeScope,
			"route-scope",
			"RouteScope",
			replacement,
			safe,
		))
	}
	return result
}

func routeAnnotationProblem(
	owner *phpsyntax.Node,
	annotation phprewrite.PHPDocAnnotation,
	kind string,
	remove string,
	replacement string,
	safe bool,
) lsp.Problem {
	return lsp.Problem{
		ID:       routeAnnotationDefaultsCode,
		Range:    annotation.Range,
		Element:  owner,
		Message:  "Shopware 6.5: migrate @" + remove + " into @Route defaults",
		Severity: protocol.DiagnosticSeverityWarning,
		Source:   "shopware-rector",
		Payload: ShopwareMigrationPayload{
			Rule:        "route-annotation-default",
			Kind:        kind,
			Safe:        safe,
			Value:       remove,
			Replacement: replacement,
		},
	}
}

func addDoctrineRouteDefault(
	annotation string,
	key string,
	value string,
) (string, bool, bool) {
	open, close, ok := doctrineAnnotationArguments(annotation)
	if !ok {
		return "", true, false
	}
	content := annotation[open+1 : close]
	valueStart, valueEnd, found := doctrineNamedValueRange(content, "defaults")
	if found {
		defaults := content[valueStart:valueEnd]
		braceOpen, braceClose, bracesOK := doctrineContainer(defaults, '{', '}')
		if !bracesOK {
			return "", true, false
		}
		inner := defaults[braceOpen+1 : braceClose]
		if _, _, exists := doctrineNamedValueRange(inner, key); exists {
			return annotation, false, true
		}
		separator := ""
		if strings.TrimSpace(inner) != "" {
			separator = ", "
		}
		updatedDefaults := defaults[:braceClose] + separator + `"` + key + `"=` + value + defaults[braceClose:]
		updatedContent := content[:valueStart] + updatedDefaults + content[valueEnd:]
		return annotation[:open+1] + updatedContent + annotation[close:], true, true
	}
	separator := ""
	if strings.TrimSpace(content) != "" {
		separator = ", "
	}
	updatedContent := content + separator + `defaults={"` + key + `"=` + value + `}`
	return annotation[:open+1] + updatedContent + annotation[close:], true, true
}

func doctrineAnnotationNamedValue(annotation, name string) (string, bool) {
	open, close, ok := doctrineAnnotationArguments(annotation)
	if !ok {
		return "", false
	}
	content := annotation[open+1 : close]
	start, end, found := doctrineNamedValueRange(content, name)
	if !found {
		return "", false
	}
	return content[start:end], true
}

func doctrineAnnotationArguments(annotation string) (int, int, bool) {
	open := strings.IndexByte(annotation, '(')
	if open < 0 {
		return 0, 0, false
	}
	close, ok := matchingDoctrineDelimiter(annotation, open, '(', ')')
	if !ok || strings.TrimSpace(annotation[close+1:]) != "" {
		return 0, 0, false
	}
	return open, close, true
}

func doctrineNamedValueRange(content, requested string) (int, int, bool) {
	for start := 0; start <= len(content); {
		end := doctrineTopLevelComma(content, start)
		segment := content[start:end]
		equals := doctrineTopLevelEquals(segment)
		if equals >= 0 {
			name := strings.Trim(strings.TrimSpace(segment[:equals]), `"'`)
			if strings.EqualFold(name, requested) {
				valueStart := start + equals + 1
				for valueStart < end && (content[valueStart] == ' ' || content[valueStart] == '\t' || content[valueStart] == '\r' || content[valueStart] == '\n') {
					valueStart++
				}
				valueEnd := end
				for valueEnd > valueStart && (content[valueEnd-1] == ' ' || content[valueEnd-1] == '\t' || content[valueEnd-1] == '\r' || content[valueEnd-1] == '\n') {
					valueEnd--
				}
				return valueStart, valueEnd, true
			}
		}
		if end == len(content) {
			break
		}
		start = end + 1
	}
	return 0, 0, false
}

func doctrineTopLevelComma(source string, start int) int {
	depth := 0
	quote := byte(0)
	escaped := false
	for index := start; index < len(source); index++ {
		character := source[index]
		if quote != 0 {
			if escaped {
				escaped = false
			} else if character == '\\' {
				escaped = true
			} else if character == quote {
				quote = 0
			}
			continue
		}
		switch character {
		case '\'', '"':
			quote = character
		case '(', '{', '[':
			depth++
		case ')', '}', ']':
			if depth > 0 {
				depth--
			}
		case ',':
			if depth == 0 {
				return index
			}
		}
	}
	return len(source)
}

func doctrineTopLevelEquals(source string) int {
	depth := 0
	quote := byte(0)
	escaped := false
	for index := 0; index < len(source); index++ {
		character := source[index]
		if quote != 0 {
			if escaped {
				escaped = false
			} else if character == '\\' {
				escaped = true
			} else if character == quote {
				quote = 0
			}
			continue
		}
		switch character {
		case '\'', '"':
			quote = character
		case '(', '{', '[':
			depth++
		case ')', '}', ']':
			if depth > 0 {
				depth--
			}
		case '=':
			if depth == 0 {
				return index
			}
		}
	}
	return -1
}

func doctrineContainer(source string, open, close byte) (int, int, bool) {
	start := strings.IndexByte(source, open)
	if start < 0 || strings.TrimSpace(source[:start]) != "" {
		return 0, 0, false
	}
	end, ok := matchingDoctrineDelimiter(source, start, open, close)
	if !ok || strings.TrimSpace(source[end+1:]) != "" {
		return 0, 0, false
	}
	return start, end, true
}

func matchingDoctrineDelimiter(
	source string,
	start int,
	open byte,
	close byte,
) (int, bool) {
	depth := 0
	quote := byte(0)
	escaped := false
	for index := start; index < len(source); index++ {
		character := source[index]
		if quote != 0 {
			if escaped {
				escaped = false
			} else if character == '\\' {
				escaped = true
			} else if character == quote {
				quote = 0
			}
			continue
		}
		if character == '\'' || character == '"' {
			quote = character
			continue
		}
		switch character {
		case open:
			depth++
		case close:
			depth--
			if depth == 0 {
				return index, true
			}
		}
	}
	return 0, false
}
