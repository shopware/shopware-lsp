package twig

import (
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
)

var templateTagNames = []string{
	"extends",
	"sw_extends",
	"include",
	"sw_include",
	"embed",
	"use",
	"from",
	"import",
	"form_theme",
}

var phpTemplateCallNames = []string{
	"render",
	"renderView",
	"renderStorefront",
	"renderBlock",
	"renderBlockView",
	"stream",
	"htmlTemplate",
	"textTemplate",
}

var templateAnnotationReferencePattern = regexp.MustCompile(
	`(?i)@Template\s*\(\s*(?:template\s*(?:=|:)\s*)?["']([^"']+\.twig)["']`,
)

type TemplateReferenceKind string

const (
	TemplateExtendsReference    TemplateReferenceKind = "extends"
	TemplateIncludeReference    TemplateReferenceKind = "include"
	TemplateEmbedReference      TemplateReferenceKind = "embed"
	TemplateImportReference     TemplateReferenceKind = "import"
	TemplateUseReference        TemplateReferenceKind = "use"
	TemplateFormThemeReference  TemplateReferenceKind = "form_theme"
	TemplateSourceReference     TemplateReferenceKind = "source"
	TemplateBlockReference      TemplateReferenceKind = "block"
	TemplateRenderReference     TemplateReferenceKind = "render"
	TemplateAttributeReference  TemplateReferenceKind = "attribute"
	TemplateAnnotationReference TemplateReferenceKind = "annotation"
)

// TemplateReference is one statically known use of a Twig template. Range
// covers only the string contents, not its quotes, so it is safe for both
// references and file-move edits.
type TemplateReference struct {
	Template string
	FilePath string
	Range    cst.TextRange
	Kind     TemplateReferenceKind
}

type TemplateReferenceCatalog struct {
	FilePath   string
	References []TemplateReference
}

func IsTwigTemplateString(node *twigsyntax.Node) bool {
	literal := twigquery.LiteralStringAt(node)
	if literal == nil || !twigquery.StringIsStatic(literal) {
		return false
	}
	if tag := twigquery.TagAt(literal); tag != nil {
		name := twigquery.TagName(tag)
		if name == "form_theme" {
			return true
		}
		if slices.Contains(templateTagNames, name) {
			// Include/extends expressions may be arrays of fallback template
			// names. Context hashes and later option expressions are excluded.
			for child := range tag.ChildNodes() {
				if child.Kind() != twigsyntax.TwigExpression {
					continue
				}
				if !twigNodeContains(child, literal) {
					return false
				}
				if twigquery.ClosestNodeOfKind(
					literal,
					twigsyntax.TwigFunctionCall,
				) != nil {
					return false
				}
				if twigquery.ClosestNodeOfKind(
					literal,
					twigsyntax.TwigLiteralHash,
				) != nil {
					return twigStringIsHashValueForKey(
						literal,
						"template",
					)
				}
				return true
			}
			return twigquery.StringInTag(literal, templateTagNames...)
		}
	}
	call := twigquery.FunctionCallAt(literal)
	if call == nil {
		return false
	}
	switch twigquery.FunctionName(call) {
	case "include", "source":
		return twigquery.FunctionArgumentIndex(literal) == 0
	case "block":
		return twigquery.FunctionArgumentIndex(literal) == 1
	default:
		return false
	}
}

func twigStringIsHashValueForKey(
	literal *twigsyntax.Node,
	key string,
) bool {
	pair := twigquery.ClosestNodeOfKind(
		literal,
		twigsyntax.TwigLiteralHashPair,
	)
	if pair == nil {
		return false
	}
	var keyNode, valueNode *twigsyntax.Node
	for child := range pair.ChildNodes() {
		switch child.Kind() {
		case twigsyntax.TwigLiteralHashKey:
			keyNode = child
		case twigsyntax.TwigLiteralHashValue, twigsyntax.TwigExpression:
			valueNode = child
		}
	}
	if keyNode == nil || valueNode == nil ||
		!twigNodeContains(valueNode, literal) {
		return false
	}
	keyText := strings.Trim(
		strings.TrimSpace(keyNode.Text()),
		`"'`+"`",
	)
	return strings.EqualFold(keyText, key)
}

func TwigTemplateStrings(root *twigsyntax.Node) []*twigsyntax.Node {
	var result []*twigsyntax.Node
	for _, literal := range twigquery.Nodes(root, twigsyntax.TwigLiteralString) {
		if IsTwigTemplateString(literal) {
			result = append(result, literal)
		}
	}
	return result
}

func TwigTemplateReferences(
	path string,
	root *twigsyntax.Node,
) []TemplateReference {
	if root == nil {
		return nil
	}
	var result []TemplateReference
	for _, literal := range TwigTemplateStrings(root) {
		template := twigquery.StringValue(literal)
		if template == "" {
			continue
		}
		result = append(result, TemplateReference{
			Template: normalizeTemplateReference(template),
			FilePath: path,
			Range:    stringContentRange(literal.Text(), literal.Range()),
			Kind:     twigTemplateReferenceKind(literal),
		})
	}
	return uniqueTemplateReferences(result)
}

func IsPHPTemplateString(node *phpsyntax.Node) bool {
	literal := phpquery.StringAt(node)
	if literal == nil || !phpStringIsStatic(literal) {
		return false
	}
	if phpTemplateCallReference(literal) {
		return true
	}
	attribute := phpquery.AttributeAt(literal)
	if attribute == nil ||
		!strings.EqualFold(filepath.Base(
			strings.ReplaceAll(phpquery.AttributeName(attribute), `\`, "/"),
		), "Template") {
		return false
	}
	index := phpquery.ArgumentIndex(attribute, literal)
	if index < 0 {
		return false
	}
	argument := phpquery.Argument(attribute, index)
	return phpquery.ArgumentExpression(attribute, index) == literal &&
		(phpquery.ArgumentName(argument) == "" && index == 0 ||
			strings.EqualFold(phpquery.ArgumentName(argument), "template"))
}

// PHPTemplateLikeString returns an exact static PHP string that looks like a
// logical Twig template name. It intentionally says nothing about semantic
// usage: callers use this only as a low-noise navigation fallback.
func PHPTemplateLikeString(node *phpsyntax.Node) (string, bool) {
	literal := phpquery.StringAt(node)
	if literal == nil || !phpStringIsStatic(literal) {
		return "", false
	}
	name := normalizeTemplateReference(phpquery.StringValue(literal))
	if name == "" || !strings.HasSuffix(strings.ToLower(name), ".twig") {
		return "", false
	}
	return name, true
}

func PHPTemplateStrings(root *phpsyntax.Node) []*phpsyntax.Node {
	var result []*phpsyntax.Node
	for _, literal := range phpquery.Nodes(root, phpsyntax.PhpString) {
		if IsPHPTemplateString(literal) {
			result = append(result, literal)
		}
	}
	return result
}

func PHPTemplateReferences(
	path string,
	root *phpsyntax.Node,
) []TemplateReference {
	if root == nil {
		return nil
	}
	var result []TemplateReference
	phpquery.Visit(root, func(literal *phpsyntax.Node) bool {
		if !IsPHPTemplateString(literal) {
			return true
		}
		template := phpquery.StringValue(literal)
		if template == "" {
			return true
		}
		kind := TemplateRenderReference
		if phpquery.AttributeAt(literal) != nil {
			kind = TemplateAttributeReference
		}
		result = append(result, TemplateReference{
			Template: normalizeTemplateReference(template),
			FilePath: path,
			Range:    stringContentRange(literal.Text(), literal.Range()),
			Kind:     kind,
		})
		return true
	}, phpsyntax.PhpString)

	source := root.Text()
	base := root.Range().Start
	for _, match := range templateAnnotationReferencePattern.FindAllStringSubmatchIndex(
		source,
		-1,
	) {
		if len(match) < 4 || match[2] < 0 {
			continue
		}
		template := source[match[2]:match[3]]
		result = append(result, TemplateReference{
			Template: normalizeTemplateReference(template),
			FilePath: path,
			Range: cst.TextRange{
				Start: base + uint32(match[2]),
				End:   base + uint32(match[3]),
			},
			Kind: TemplateAnnotationReference,
		})
	}
	return uniqueTemplateReferences(result)
}

func TemplateReferenceAt(
	references []TemplateReference,
	offset uint32,
) (TemplateReference, bool) {
	for _, reference := range references {
		if offset >= reference.Range.Start && offset <= reference.Range.End {
			return reference, true
		}
	}
	return TemplateReference{}, false
}

func normalizeTemplateReference(template string) string {
	return strings.TrimPrefix(
		filepath.ToSlash(strings.TrimSpace(template)),
		"/",
	)
}

func twigTemplateReferenceKind(
	literal *twigsyntax.Node,
) TemplateReferenceKind {
	if tag := twigquery.TagAt(literal); tag != nil {
		switch twigquery.TagName(tag) {
		case "extends", "sw_extends":
			return TemplateExtendsReference
		case "include", "sw_include":
			return TemplateIncludeReference
		case "embed":
			return TemplateEmbedReference
		case "use":
			return TemplateUseReference
		case "from", "import":
			return TemplateImportReference
		case "form_theme":
			return TemplateFormThemeReference
		}
	}
	switch twigquery.FunctionName(twigquery.FunctionCallAt(literal)) {
	case "source":
		return TemplateSourceReference
	case "block":
		return TemplateBlockReference
	default:
		return TemplateIncludeReference
	}
}

func phpTemplateCallReference(literal *phpsyntax.Node) bool {
	call := phpquery.CallAt(literal)
	if call == nil ||
		!slices.ContainsFunc(phpTemplateCallNames, func(name string) bool {
			return strings.EqualFold(name, phpquery.CallMethodName(call))
		}) {
		return false
	}
	index := phpquery.ArgumentIndex(call, literal)
	if index < 0 {
		return false
	}
	argument := phpquery.Argument(call, index)
	name := strings.ToLower(phpquery.ArgumentName(argument))
	return phpquery.ArgumentExpression(call, index) == literal &&
		(index == 0 || name == "view" || name == "name" || name == "template")
}

func phpStringIsStatic(literal *phpsyntax.Node) bool {
	text := strings.TrimSpace(literal.Text())
	return len(text) >= 2 &&
		(text[0] == '\'' || text[0] == '"' || text[0] == '`') &&
		(text[0] == '\'' || !strings.Contains(text[1:len(text)-1], "$"))
}

func stringContentRange(text string, rng cst.TextRange) cst.TextRange {
	leading := len(text) - len(strings.TrimLeft(text, " \t\r\n"))
	trimmed := strings.TrimSpace(text)
	if len(trimmed) >= 2 {
		quote := trimmed[0]
		if (quote == '\'' || quote == '"' || quote == '`') &&
			trimmed[len(trimmed)-1] == quote {
			return cst.TextRange{
				Start: rng.Start + uint32(leading+1),
				End:   rng.Start + uint32(leading+len(trimmed)-1),
			}
		}
	}
	return rng
}

func twigNodeContains(ancestor, node *twigsyntax.Node) bool {
	if ancestor == nil || node == nil {
		return false
	}
	ancestorRange := ancestor.Range()
	nodeRange := node.Range()
	return nodeRange.Start >= ancestorRange.Start &&
		nodeRange.End <= ancestorRange.End
}

func uniqueTemplateReferences(
	references []TemplateReference,
) []TemplateReference {
	seen := make(map[string]struct{}, len(references))
	result := make([]TemplateReference, 0, len(references))
	for _, reference := range references {
		key := reference.FilePath + "\x00" +
			reference.Range.String() + "\x00" +
			reference.Template
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, reference)
	}
	return result
}
