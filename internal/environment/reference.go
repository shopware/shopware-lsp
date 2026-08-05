package environment

import (
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
)

const envPrefix = "%env("

func References(source string) []Reference {
	var result []Reference
	for cursor := 0; cursor < len(source); {
		relativeStart := strings.Index(source[cursor:], envPrefix)
		if relativeStart < 0 {
			break
		}
		start := cursor + relativeStart
		expressionStart := start + len(envPrefix)
		relativeEnd := strings.Index(source[expressionStart:], ")%")
		if relativeEnd < 0 {
			break
		}
		if nested := strings.Index(
			source[expressionStart:expressionStart+relativeEnd],
			envPrefix,
		); nested >= 0 {
			cursor = expressionStart + nested
			continue
		}
		expressionEnd := expressionStart + relativeEnd
		if reference, found := parseReference(
			source[expressionStart:expressionEnd],
			uint32(start),
			uint32(expressionStart),
			uint32(expressionEnd+2),
		); found {
			result = append(result, reference)
		}
		cursor = expressionEnd + 2
	}
	return result
}

func ReferenceAt(
	source string,
	offset uint32,
) (Reference, bool) {
	for _, reference := range References(source) {
		if containsInclusive(reference.NameRange, offset) {
			return reference, true
		}
	}
	return Reference{}, false
}

func OccurrenceAt(
	path,
	source string,
	offset uint32,
) (Occurrence, bool) {
	if reference, found := ReferenceAt(source, offset); found {
		return Occurrence{
			Kind:       ReferenceOccurrence,
			Source:     SymfonyEnvSource,
			Name:       reference.Name,
			Range:      reference.Range,
			NameRange:  reference.NameRange,
			Processors: reference.Processors,
		}, true
	}
	var declarations []Occurrence
	switch {
	case isDotEnvPath(path):
		declarations = parseDotEnv(source)
	case isDockerfilePath(path):
		declarations = parseDockerfile(source)
	case isDockerComposePath(path):
		declarations = parseDockerCompose(source)
	default:
		if !supportsSymfonyEnvReference(path) {
			return Occurrence{}, false
		}
	}
	for _, declaration := range declarations {
		if containsInclusive(declaration.NameRange, offset) {
			return declaration, true
		}
	}
	return Occurrence{}, false
}

func CompletionReferenceAt(
	source string,
	offset uint32,
) (Reference, bool) {
	if int(offset) > len(source) {
		offset = uint32(len(source))
	}
	cursorOffset := int(offset)
	before := source[:cursorOffset]
	start := strings.LastIndex(before, envPrefix)
	if start < 0 {
		return Reference{}, false
	}
	expressionStart := start + len(envPrefix)
	if strings.Contains(source[expressionStart:cursorOffset], ")%") {
		return Reference{}, false
	}
	if close := strings.Index(source[expressionStart:], ")%"); close >= 0 &&
		expressionStart+close < cursorOffset {
		return Reference{}, false
	}
	expression := source[expressionStart:cursorOffset]
	segmentStart := strings.LastIndexByte(expression, ':') + 1
	namePart := expression[segmentStart:]
	leftTrimmed := strings.TrimLeft(namePart, " \t")
	nameStart := int(offset) - len(namePart) +
		(len(namePart) - len(leftTrimmed))
	name := strings.TrimSpace(leftTrimmed)
	for index := 0; index < len(name); index++ {
		if !isNamePart(name[index]) {
			return Reference{}, false
		}
	}
	return Reference{
		Name:       name,
		Range:      cst.TextRange{Start: uint32(start), End: offset},
		NameRange:  cst.TextRange{Start: uint32(nameStart), End: offset},
		Processors: referenceProcessors(expression[:segmentStart]),
	}, true
}

// PHPReferences returns direct PHP environment references configured through
// env('resolve:APP_ENV') or #[Autowire(env: 'resolve:APP_ENV')]. Symfony
// accepts the processor chain without the %env(...)% wrapper in these forms.
func PHPReferences(root *phpsyntax.Node) []Reference {
	var result []Reference
	for _, literal := range phpquery.Nodes(root, phpsyntax.PhpString) {
		if reference, found := phpReference(literal, false); found {
			result = append(result, reference)
		}
	}
	return result
}

// PHPReferenceAt recognizes a complete direct PHP environment reference under
// the cursor.
func PHPReferenceAt(
	node *phpsyntax.Node,
	offset uint32,
) (Reference, bool) {
	reference, found := phpReference(node, false)
	if !found || !containsInclusive(reference.NameRange, offset) {
		return Reference{}, false
	}
	return reference, true
}

// PHPCompletionReferenceAt recognizes the editable environment-name segment,
// including an empty string while completion is being requested.
func PHPCompletionReferenceAt(
	node *phpsyntax.Node,
	offset uint32,
) (Reference, bool) {
	reference, found := phpReference(node, true)
	if !found || !containsInclusive(reference.NameRange, offset) {
		return Reference{}, false
	}
	return reference, true
}

func PHPOccurrenceAt(
	node *phpsyntax.Node,
	offset uint32,
) (Occurrence, bool) {
	reference, found := PHPReferenceAt(node, offset)
	if !found {
		return Occurrence{}, false
	}
	return Occurrence{
		Kind:       ReferenceOccurrence,
		Source:     SymfonyEnvSource,
		Name:       reference.Name,
		Range:      reference.Range,
		NameRange:  reference.NameRange,
		Processors: reference.Processors,
	}, true
}

func phpReference(
	node *phpsyntax.Node,
	allowEmpty bool,
) (Reference, bool) {
	literal := phpquery.StringAt(node)
	if literal == nil || !phpReferenceContext(literal) {
		return Reference{}, false
	}

	value := phpquery.StringValue(literal)
	segmentStart := strings.LastIndexByte(value, ':') + 1
	namePart := value[segmentStart:]
	leftTrimmed := strings.TrimLeft(namePart, " \t")
	name := strings.TrimSpace(leftTrimmed)
	if name == "" {
		if !allowEmpty {
			return Reference{}, false
		}
	} else if !validName(name) {
		return Reference{}, false
	}
	contentRange := phpquery.StringContentRange(literal)
	nameStart := contentRange.Start + uint32(segmentStart) +
		uint32(len(namePart)-len(leftTrimmed))
	return Reference{
		Name:  name,
		Range: contentRange,
		NameRange: cst.TextRange{
			Start: nameStart,
			End:   nameStart + uint32(len(name)),
		},
		Processors: referenceProcessors(value[:segmentStart]),
	}, true
}

func phpReferenceContext(literal *phpsyntax.Node) bool {
	if attribute := phpquery.AttributeAt(literal); attribute != nil &&
		shortPHPName(phpquery.AttributeName(attribute)) == "Autowire" {
		index := phpquery.ArgumentIndex(attribute, literal)
		return index >= 0 &&
			phpquery.ArgumentName(phpquery.Argument(attribute, index)) == "env"
	}
	call := phpquery.CallAt(literal)
	return call != nil &&
		call.Kind() == phpsyntax.PhpFunctionCall &&
		phpquery.StringArgumentIndex(literal) == 0 &&
		shortPHPName(phpquery.CallName(call)) == "env"
}

func shortPHPName(name string) string {
	if index := strings.LastIndexByte(name, '\\'); index >= 0 {
		return name[index+1:]
	}
	return name
}

func parseReference(
	expression string,
	start,
	expressionStart,
	end uint32,
) (Reference, bool) {
	segmentStart := strings.LastIndexByte(expression, ':') + 1
	namePart := expression[segmentStart:]
	leftTrimmed := strings.TrimLeft(namePart, " \t")
	name := strings.TrimSpace(leftTrimmed)
	if !validName(name) {
		return Reference{}, false
	}
	nameStart := expressionStart + uint32(segmentStart) +
		uint32(len(namePart)-len(leftTrimmed))
	return Reference{
		Name: name,
		Range: cst.TextRange{
			Start: start,
			End:   end,
		},
		NameRange: cst.TextRange{
			Start: nameStart,
			End:   nameStart + uint32(len(name)),
		},
		Processors: referenceProcessors(expression[:segmentStart]),
	}, true
}

func referenceProcessors(value string) []string {
	value = strings.TrimSuffix(value, ":")
	if value == "" {
		return nil
	}
	parts := strings.Split(value, ":")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			result = append(result, part)
		}
	}
	return result
}

func containsInclusive(rng cst.TextRange, offset uint32) bool {
	return offset >= rng.Start && offset <= rng.End
}
