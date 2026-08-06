package twig

import (
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	twigast "github.com/shopware/shopware-lsp/internal/parser/twig/ast"
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

// TwigTemplateTarget is one statically known candidate produced by a Twig
// template expression. Range covers the string contents for a direct literal
// and the complete expression for a constant-folded value.
type TwigTemplateTarget struct {
	Template string
	Range    cst.TextRange

	directLiteral bool
}

// TwigTemplateTargetGroup describes all possible templates produced by one
// Twig template expression. Exact is false when any runtime value participates
// in the expression. Fallback arrays are represented as one group so callers
// can apply Twig's "first existing template" semantics.
type TwigTemplateTargetGroup struct {
	Targets       []TwigTemplateTarget
	Range         cst.TextRange
	Exact         bool
	IgnoreMissing bool
	Kind          TemplateReferenceKind
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
	for _, group := range TwigTemplateTargetGroups(root) {
		for _, target := range group.Targets {
			// A constant concatenation is useful for validation, but its range
			// is not safe for reference rename or file-move edits.
			if !target.directLiteral || target.Template == "" {
				continue
			}
			result = append(result, TemplateReference{
				Template: target.Template,
				FilePath: path,
				Range:    target.Range,
				Kind:     group.Kind,
			})
		}
	}
	return uniqueTemplateReferences(result)
}

// TwigTemplateTargetGroups evaluates template-bearing Twig expressions. It
// folds static string concatenations, preserves fallback arrays as groups, and
// marks expressions involving runtime values as inexact. Literal fragments of
// dynamic expressions are intentionally not exposed as template targets.
func TwigTemplateTargetGroups(
	root *twigsyntax.Node,
) []TwigTemplateTargetGroup {
	if root == nil {
		return nil
	}

	kinds := []twigsyntax.Kind{
		twigsyntax.TwigExtends,
		twigsyntax.TwigInclude,
		twigsyntax.TwigUse,
		twigsyntax.TwigEmbed,
		twigsyntax.TwigFrom,
		twigsyntax.TwigImport,
		twigsyntax.TwigFormTheme,
		twigsyntax.ShopwareTwigSwExtends,
		twigsyntax.ShopwareTwigSwInclude,
		twigsyntax.TwigFunctionCall,
	}
	var result []TwigTemplateTargetGroup
	for _, node := range twigquery.Nodes(root, kinds...) {
		if node.Kind() == twigsyntax.TwigFunctionCall {
			if group, ok := twigFunctionTemplateTargetGroup(node); ok {
				result = append(result, group)
			}
			continue
		}
		result = append(result, twigTagTemplateTargetGroups(node)...)
	}
	return result
}

type twigTemplateEvaluation struct {
	targets     []TwigTemplateTarget
	exact       bool
	scalar      string
	scalarKnown bool
}

func twigTagTemplateTargetGroups(
	tag *twigsyntax.Node,
) []TwigTemplateTargetGroup {
	name := twigquery.TagName(tag)
	if !slices.Contains(templateTagNames, name) {
		return nil
	}

	scope := tag
	if name == "embed" {
		for child := range tag.ChildNodes() {
			if child.Kind() == twigsyntax.TwigEmbedStartingBlock {
				scope = child
				break
			}
		}
	}

	var targetNodes []*twigsyntax.Node
	if name == "use" {
		for child := range scope.ChildNodes() {
			if child.Kind() == twigsyntax.TwigLiteralString {
				targetNodes = append(targetNodes, child)
				break
			}
		}
	} else {
		var expressions []*twigsyntax.Node
		for child := range scope.ChildNodes() {
			if child.Kind() == twigsyntax.TwigExpression {
				expressions = append(expressions, child)
			}
		}
		if name == "form_theme" {
			if len(expressions) > 1 {
				targetNodes = append(targetNodes, expressions[1:]...)
			}
		} else if len(expressions) != 0 {
			targetNodes = append(targetNodes, expressions[0])
		}
	}

	if name == "sw_extends" && len(targetNodes) != 0 {
		if value := twigHashValueForKey(targetNodes[0], "template"); value != nil {
			targetNodes[0] = value
		}
	}
	if name == "form_theme" {
		var expanded []*twigsyntax.Node
		for _, targetNode := range targetNodes {
			items := twigArrayExpressionItems(targetNode)
			if len(items) == 0 {
				expanded = append(expanded, targetNode)
				continue
			}
			expanded = append(expanded, items...)
		}
		targetNodes = expanded
	}

	ignoreMissing := twigNodeContainsToken(scope, twigsyntax.TkIgnoreMissing)
	kind := twigTemplateReferenceKindForName(name)
	result := make([]TwigTemplateTargetGroup, 0, len(targetNodes))
	for _, targetNode := range targetNodes {
		result = append(result, newTwigTemplateTargetGroup(
			targetNode,
			kind,
			ignoreMissing,
		))
	}
	return result
}

func twigFunctionTemplateTargetGroup(
	call *twigsyntax.Node,
) (TwigTemplateTargetGroup, bool) {
	name := twigquery.FunctionName(call)
	var target *twigsyntax.Node
	var kind TemplateReferenceKind
	var ignoreMissing bool
	switch name {
	case "include":
		target = twigFunctionArgument(call, 0, "template")
		kind = TemplateIncludeReference
		ignoreMissing = twigFunctionArgumentIsTrue(
			twigFunctionArgument(call, 3, "ignore_missing"),
		)
	case "source":
		target = twigFunctionArgument(call, 0, "name")
		kind = TemplateSourceReference
		ignoreMissing = twigFunctionArgumentIsTrue(
			twigFunctionArgument(call, 1, "ignore_missing"),
		)
	case "block":
		target = twigFunctionArgument(call, 1, "template")
		kind = TemplateBlockReference
	default:
		return TwigTemplateTargetGroup{}, false
	}
	if target == nil {
		return TwigTemplateTargetGroup{}, false
	}
	return newTwigTemplateTargetGroup(target, kind, ignoreMissing), true
}

func twigFunctionArgument(
	call *twigsyntax.Node,
	position int,
	name string,
) *twigsyntax.Node {
	for index := 0; ; index++ {
		argument := twigquery.FunctionArgument(call, index)
		if argument == nil {
			break
		}
		if argument.Kind() != twigsyntax.TwigNamedArgument {
			continue
		}
		nameToken := argument.ChildTokenOfKind(twigsyntax.TkWord)
		if nameToken != nil && strings.EqualFold(nameToken.Text(), name) {
			return argument
		}
	}
	return twigquery.FunctionArgument(call, position)
}

func twigFunctionArgumentIsTrue(argument *twigsyntax.Node) bool {
	if argument == nil {
		return false
	}
	if argument.Kind() == twigsyntax.TwigNamedArgument {
		for child := range argument.ChildNodes() {
			if child.Kind() == twigsyntax.TwigExpression {
				argument = child
				break
			}
		}
	}
	return strings.EqualFold(strings.TrimSpace(argument.Text()), "true")
}

func newTwigTemplateTargetGroup(
	node *twigsyntax.Node,
	kind TemplateReferenceKind,
	ignoreMissing bool,
) TwigTemplateTargetGroup {
	evaluation := evaluateTwigTemplateTarget(node)
	rng := node.RangeTrimmedTrivia()
	if len(evaluation.targets) == 1 && evaluation.targets[0].directLiteral {
		rng = evaluation.targets[0].Range
	}
	return TwigTemplateTargetGroup{
		Targets:       evaluation.targets,
		Range:         rng,
		Exact:         evaluation.exact,
		IgnoreMissing: ignoreMissing,
		Kind:          kind,
	}
}

func evaluateTwigTemplateTarget(
	node *twigsyntax.Node,
) twigTemplateEvaluation {
	if node == nil {
		return twigTemplateEvaluation{}
	}
	switch node.Kind() {
	case twigsyntax.TwigExpression,
		twigsyntax.TwigParenthesesExpression,
		twigsyntax.TwigNamedArgument,
		twigsyntax.TwigLiteralHashValue:
		for child := range node.ChildNodes() {
			return evaluateTwigTemplateTarget(child)
		}
		return twigTemplateEvaluation{}
	case twigsyntax.TwigLiteralString:
		if !twigquery.StringIsStatic(node) {
			return twigTemplateEvaluation{}
		}
		value := twigquery.StringValue(node)
		name := normalizeTemplateReference(value)
		if name == "" {
			return twigTemplateEvaluation{
				exact:       true,
				scalar:      value,
				scalarKnown: true,
			}
		}
		return twigTemplateEvaluation{
			targets: []TwigTemplateTarget{{
				Template:      name,
				Range:         stringContentRange(node.Text(), node.Range()),
				directLiteral: true,
			}},
			exact:       true,
			scalar:      value,
			scalarKnown: true,
		}
	case twigsyntax.TwigBinaryExpression:
		binary, ok := twigast.CastTwigBinaryExpression(node)
		if !ok || binary.Operator() == nil ||
			strings.TrimSpace(binary.Operator().Text()) != "~" {
			return twigTemplateEvaluation{}
		}
		lhs, lhsOK := binary.LhsExpression()
		rhs, rhsOK := binary.RhsExpression()
		if !lhsOK || !rhsOK {
			return twigTemplateEvaluation{}
		}
		left := evaluateTwigTemplateTarget(lhs.Syntax())
		right := evaluateTwigTemplateTarget(rhs.Syntax())
		if !left.scalarKnown || !right.scalarKnown {
			return twigTemplateEvaluation{}
		}
		value := left.scalar + right.scalar
		name := normalizeTemplateReference(value)
		var targets []TwigTemplateTarget
		if name != "" {
			targets = []TwigTemplateTarget{{
				Template: name,
				Range:    node.RangeTrimmedTrivia(),
			}}
		}
		return twigTemplateEvaluation{
			targets:     targets,
			exact:       true,
			scalar:      value,
			scalarKnown: true,
		}
	case twigsyntax.TwigLiteralArray:
		var inner *twigsyntax.Node
		for child := range node.ChildNodes() {
			if child.Kind() == twigsyntax.TwigLiteralArrayInner {
				inner = child
				break
			}
		}
		if inner == nil {
			return twigTemplateEvaluation{}
		}
		result := twigTemplateEvaluation{exact: true}
		for child := range inner.ChildNodes() {
			if child.Kind() != twigsyntax.TwigExpression {
				continue
			}
			item := evaluateTwigTemplateTarget(child)
			if !item.scalarKnown {
				result.exact = false
			}
			result.targets = append(result.targets, item.targets...)
		}
		return result
	default:
		return twigTemplateEvaluation{}
	}
}

func twigHashValueForKey(
	node *twigsyntax.Node,
	key string,
) *twigsyntax.Node {
	for _, pair := range twigquery.Nodes(
		node,
		twigsyntax.TwigLiteralHashPair,
	) {
		var keyNode, valueNode *twigsyntax.Node
		for child := range pair.ChildNodes() {
			switch child.Kind() {
			case twigsyntax.TwigLiteralHashKey:
				keyNode = child
			case twigsyntax.TwigLiteralHashValue, twigsyntax.TwigExpression:
				valueNode = child
			}
		}
		if keyNode == nil || valueNode == nil {
			continue
		}
		keyText := strings.Trim(
			strings.TrimSpace(keyNode.Text()),
			`"'`+"`",
		)
		if strings.EqualFold(keyText, key) {
			return valueNode
		}
	}
	return nil
}

func twigArrayExpressionItems(node *twigsyntax.Node) []*twigsyntax.Node {
	for node != nil {
		switch node.Kind() {
		case twigsyntax.TwigExpression,
			twigsyntax.TwigParenthesesExpression,
			twigsyntax.TwigNamedArgument,
			twigsyntax.TwigLiteralHashValue:
			var child *twigsyntax.Node
			for candidate := range node.ChildNodes() {
				child = candidate
				break
			}
			node = child
			continue
		}
		break
	}
	if node == nil || node.Kind() != twigsyntax.TwigLiteralArray {
		return nil
	}
	for child := range node.ChildNodes() {
		if child.Kind() != twigsyntax.TwigLiteralArrayInner {
			continue
		}
		var result []*twigsyntax.Node
		for item := range child.ChildNodes() {
			if item.Kind() == twigsyntax.TwigExpression {
				result = append(result, item)
			}
		}
		return result
	}
	return nil
}

func twigNodeContainsToken(node *twigsyntax.Node, kind twigsyntax.Kind) bool {
	if node == nil {
		return false
	}
	for descendant := range node.Descendants() {
		token, ok := descendant.(*twigsyntax.Token)
		if ok && token.Kind() == kind {
			return true
		}
	}
	return false
}

func twigTemplateReferenceKindForName(name string) TemplateReferenceKind {
	switch name {
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
	default:
		return TemplateIncludeReference
	}
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
