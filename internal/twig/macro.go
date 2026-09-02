package twig

import (
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	twigast "github.com/shopware/shopware-lsp/internal/parser/twig/ast"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
)

type MacroParameter struct {
	Name          string
	Default       string
	Documentation string
	Range         cst.TextRange
}

type Macro struct {
	Name          string
	Parameters    []MacroParameter
	Documentation string
	FilePath      string
	Templates     []string
	Range         cst.TextRange
	NameRange     cst.TextRange
}

func (macro Macro) Signature() string {
	parameters := make([]string, 0, len(macro.Parameters))
	for _, parameter := range macro.Parameters {
		value := parameter.Name
		if parameter.Default != "" {
			value += " = " + parameter.Default
		}
		parameters = append(parameters, value)
	}
	return macro.Name + "(" + strings.Join(parameters, ", ") + ")"
}

type MacroCatalog struct {
	Template string
	FilePath string
	Macros   []Macro
}

type MacroUsage struct {
	Template string
	Name     string
	FilePath string
	Range    cst.TextRange
}

type MacroUsageRecord struct {
	Template string
	Name     string
	FilePath string
	Usages   []MacroUsage
}

type MacroReferenceRole uint8

const (
	MacroDeclarationReference MacroReferenceRole = iota
	MacroUsageReference
)

type MacroReference struct {
	Name      string
	Templates []string
	Range     cst.TextRange
	Role      MacroReferenceRole
}

type MacroImport struct {
	Alias     string
	Name      string
	Templates []string
	Range     cst.TextRange
}

type MacroCompletionContext struct {
	Templates []string
	Direct    []MacroImport
}

type macroImports struct {
	namespaces map[string][]string
	direct     map[string]MacroImport
	references []MacroReference
}

func MacrosInDocument(
	path string,
	root *twigsyntax.Node,
) []Macro {
	if root == nil {
		return nil
	}
	templates := TemplateNames(path)
	var result []Macro
	for node := range twigquery.IterateNodes(root, twigsyntax.TwigMacro) {
		start := directChild(node, twigsyntax.TwigMacroStartingBlock)
		if start == nil {
			continue
		}
		nameToken := tokenAfter(start, twigsyntax.TkMacro, twigsyntax.TkWord)
		if nameToken == nil || nameToken.Text() == "" {
			continue
		}
		macro := Macro{
			Name:          nameToken.Text(),
			Documentation: DocumentationBefore(node),
			FilePath:      path,
			Templates:     append([]string(nil), templates...),
			Range:         node.RangeTrimmedTrivia(),
			NameRange:     nameToken.Range(),
		}
		arguments := directChild(start, twigsyntax.TwigArguments)
		if arguments != nil {
			var parameterDocumentation []string
			for child := range arguments.ChildNodes() {
				switch child.Kind() {
				case twigsyntax.TwigComment:
					if documentation, documented := DocumentationCommentText(
						child.Text(),
					); documented && documentation != "" {
						parameterDocumentation = append(
							parameterDocumentation,
							documentation,
						)
					}
				case twigsyntax.TwigExpression, twigsyntax.TwigNamedArgument:
					if parameter, found := macroParameter(child); found {
						parameter.Documentation = strings.Join(
							parameterDocumentation,
							"\n",
						)
						macro.Parameters = append(macro.Parameters, parameter)
					}
					parameterDocumentation = nil
				}
			}
		}
		result = append(result, macro)
	}
	return result
}

func MacroReferencesInDocument(
	path string,
	root *twigsyntax.Node,
) []MacroReference {
	if root == nil {
		return nil
	}
	var result []MacroReference
	for _, macro := range MacrosInDocument(path, root) {
		result = append(result, MacroReference{
			Name:      macro.Name,
			Templates: macro.Templates,
			Range:     macro.NameRange,
			Role:      MacroDeclarationReference,
		})
	}
	imports := collectMacroImports(path, root)
	result = append(result, imports.references...)
	for call := range twigquery.IterateNodes(root, twigsyntax.TwigFunctionCall) {
		nameOperand := functionNameOperand(call)
		names := literalNames(nameOperand)
		if len(names) == 0 {
			continue
		}
		if len(names) >= 2 {
			namespace := names[0].name
			templates := imports.namespaces[strings.ToLower(namespace)]
			if len(templates) == 0 && namespace == "_self" {
				templates = TemplateNames(path)
			}
			if len(templates) == 0 {
				continue
			}
			name := names[len(names)-1]
			result = append(result, MacroReference{
				Name:      name.name,
				Templates: append([]string(nil), templates...),
				Range:     name.rng,
				Role:      MacroUsageReference,
			})
			continue
		}
		target, found := imports.direct[strings.ToLower(names[0].name)]
		if !found {
			continue
		}
		result = append(result, MacroReference{
			Name:      target.Name,
			Templates: append([]string(nil), target.Templates...),
			Range:     names[0].rng,
			Role:      MacroUsageReference,
		})
	}
	return uniqueMacroReferences(result)
}

func MacroReferenceAt(
	path string,
	root,
	node *twigsyntax.Node,
	offset uint32,
) (MacroReference, bool) {
	if root == nil || node == nil {
		return MacroReference{}, false
	}
	for _, reference := range MacroReferencesInDocument(path, root) {
		if offset >= reference.Range.Start && offset <= reference.Range.End {
			return reference, true
		}
	}
	return MacroReference{}, false
}

func MacroCompletionAt(
	path string,
	root,
	node *twigsyntax.Node,
) (MacroCompletionContext, bool) {
	if root == nil || node == nil {
		return MacroCompletionContext{}, false
	}
	imports := collectMacroImports(path, root)
	call := twigquery.FunctionCallAt(node)
	if call != nil {
		names := literalNames(functionNameOperand(call))
		if len(names) >= 2 {
			templates := imports.namespaces[strings.ToLower(names[0].name)]
			if len(templates) == 0 && names[0].name == "_self" {
				templates = TemplateNames(path)
			}
			if len(templates) != 0 {
				return MacroCompletionContext{
					Templates: append([]string(nil), templates...),
				}, true
			}
		}
	}
	accessor := twigquery.ClosestNodeOfKind(
		node,
		twigsyntax.TwigAccessor,
	)
	if accessor == nil {
		scope := twigquery.ClosestNodeOfKind(node, twigsyntax.TwigVar)
		for candidate := range twigquery.IterateNodes(
			scope,
			twigsyntax.TwigAccessor,
		) {
			if strings.Contains(candidate.Text(), ".") {
				accessor = candidate
			}
		}
	}
	if accessor != nil && strings.Contains(accessor.Text(), ".") {
		names := literalNames(accessor)
		if len(names) != 0 {
			templates := imports.namespaces[strings.ToLower(names[0].name)]
			if len(templates) == 0 && names[0].name == "_self" {
				templates = TemplateNames(path)
			}
			if len(templates) != 0 {
				return MacroCompletionContext{
					Templates: append([]string(nil), templates...),
				}, true
			}
		}
	}
	if len(imports.direct) == 0 {
		return MacroCompletionContext{}, false
	}
	result := MacroCompletionContext{
		Direct: make([]MacroImport, 0, len(imports.direct)),
	}
	for _, current := range imports.direct {
		result.Direct = append(result.Direct, current)
	}
	sort.Slice(result.Direct, func(left, right int) bool {
		return compareFold(
			result.Direct[left].Alias,
			result.Direct[right].Alias,
		) < 0
	})
	return result, true
}

func collectMacroImports(
	path string,
	root *twigsyntax.Node,
) macroImports {
	result := macroImports{
		namespaces: make(map[string][]string),
		direct:     make(map[string]MacroImport),
	}
	for node := range twigquery.IterateNodes(
		root,
		twigsyntax.TwigImport,
	) {
		templates, names := macroImportTarget(path, node)
		if len(templates) == 0 || len(names) == 0 {
			continue
		}
		alias := names[len(names)-1]
		result.namespaces[strings.ToLower(alias.name)] =
			append([]string(nil), templates...)
	}
	for node := range twigquery.IterateNodes(root, twigsyntax.TwigFrom) {
		templates, _ := macroImportTarget(path, node)
		if len(templates) == 0 {
			continue
		}
		for override := range twigquery.IterateNodes(
			node,
			twigsyntax.TwigOverride,
		) {
			names := literalNames(override)
			if len(names) == 0 {
				continue
			}
			original := names[0]
			alias := names[len(names)-1]
			current := MacroImport{
				Alias:     alias.name,
				Name:      original.name,
				Templates: append([]string(nil), templates...),
				Range:     alias.rng,
			}
			result.direct[strings.ToLower(alias.name)] = current
			result.references = append(
				result.references,
				MacroReference{
					Name:      original.name,
					Templates: append([]string(nil), templates...),
					Range:     original.rng,
					Role:      MacroUsageReference,
				},
			)
		}
	}
	return result
}

func macroImportTarget(
	path string,
	node *twigsyntax.Node,
) ([]string, []macroName) {
	for literal := range twigquery.IterateNodes(
		node,
		twigsyntax.TwigLiteralString,
	) {
		if twigquery.StringIsStatic(literal) {
			name := twigquery.StringValue(literal)
			if name != "" {
				return []string{name}, literalNames(node)
			}
		}
	}
	names := literalNames(node)
	if len(names) != 0 && names[0].name == "_self" {
		return TemplateNames(path), names
	}
	return nil, names
}

type macroName struct {
	name string
	rng  cst.TextRange
}

func literalNames(node *twigsyntax.Node) []macroName {
	if node == nil {
		return nil
	}
	var result []macroName
	for literal := range twigquery.IterateNodes(
		node,
		twigsyntax.TwigLiteralName,
	) {
		name, rng := literalName(literal)
		if name != "" {
			result = append(result, macroName{name: name, rng: rng})
		}
	}
	return result
}

func literalName(node *twigsyntax.Node) (string, cst.TextRange) {
	for token := range node.ChildTokens() {
		if token.Kind() == twigsyntax.TkWord {
			return token.Text(), token.Range()
		}
	}
	return strings.TrimSpace(node.Text()), node.RangeTrimmedTrivia()
}

func macroParameter(
	node *twigsyntax.Node,
) (MacroParameter, bool) {
	if node == nil {
		return MacroParameter{}, false
	}
	var name string
	var rng cst.TextRange
	if node.Kind() == twigsyntax.TwigNamedArgument {
		for token := range node.ChildTokens() {
			if token.Kind() == twigsyntax.TkWord {
				name = token.Text()
				rng = token.Range()
				break
			}
		}
	} else {
		names := literalNames(node)
		if len(names) != 0 {
			name = names[0].name
			rng = names[0].rng
		}
	}
	if name == "" {
		return MacroParameter{}, false
	}
	parameter := MacroParameter{Name: name, Range: rng}
	if node.Kind() == twigsyntax.TwigNamedArgument {
		if equal := strings.IndexByte(node.Text(), '='); equal >= 0 {
			parameter.Default = strings.TrimSpace(node.Text()[equal+1:])
		}
	}
	return parameter, true
}

func functionNameOperand(node *twigsyntax.Node) *twigsyntax.Node {
	call, ok := twigast.CastTwigFunctionCall(node)
	if !ok {
		return nil
	}
	operand, ok := call.NameOperand()
	if !ok {
		return nil
	}
	return operand.Syntax()
}

func directChild(
	node *twigsyntax.Node,
	kind twigsyntax.Kind,
) *twigsyntax.Node {
	if node == nil {
		return nil
	}
	for child := range node.ChildNodes() {
		if child.Kind() == kind {
			return child
		}
	}
	return nil
}

func tokenAfter(
	node *twigsyntax.Node,
	after,
	kind twigsyntax.Kind,
) *twigsyntax.Token {
	found := false
	for token := range node.ChildTokens() {
		if token.Kind() == after {
			found = true
			continue
		}
		if found && token.Kind() == kind {
			return token
		}
	}
	return nil
}

func uniqueMacroReferences(values []MacroReference) []MacroReference {
	seen := make(map[string]struct{}, len(values))
	result := make([]MacroReference, 0, len(values))
	for _, value := range values {
		if value.Name == "" || len(value.Templates) == 0 {
			continue
		}
		key := strings.ToLower(value.Name) + ":" +
			strings.Join(value.Templates, "\x00") + ":" +
			value.Range.String() + ":" + string(rune(value.Role))
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, value)
	}
	return result
}

func normalizeMacroTemplate(template string) string {
	return strings.TrimPrefix(
		strings.ReplaceAll(strings.TrimSpace(template), `\`, "/"),
		"/",
	)
}

func macroUsageKey(template, name string) string {
	return normalizeMacroTemplate(template) + "\x00" +
		strings.ToLower(strings.TrimSpace(name))
}

func sortMacros(macros []Macro) {
	sort.Slice(macros, func(left, right int) bool {
		if macros[left].Name != macros[right].Name {
			return compareFold(
				macros[left].Name,
				macros[right].Name,
			) < 0
		}
		if macros[left].FilePath != macros[right].FilePath {
			return macros[left].FilePath < macros[right].FilePath
		}
		return macros[left].NameRange.Start < macros[right].NameRange.Start
	})
}

func sortMacroUsages(usages []MacroUsage) {
	sort.Slice(usages, func(left, right int) bool {
		if usages[left].FilePath != usages[right].FilePath {
			return usages[left].FilePath < usages[right].FilePath
		}
		return usages[left].Range.Start < usages[right].Range.Start
	})
}
