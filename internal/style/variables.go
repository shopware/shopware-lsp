package style

import (
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	scssquery "github.com/shopware/shopware-lsp/internal/parser/scss/query"
	scsssyntax "github.com/shopware/shopware-lsp/internal/parser/scss/syntax"
	"github.com/shopware/shopware-lsp/internal/theme"
)

type VariableOccurrenceKind uint8

const (
	VariableGlobalDeclaration VariableOccurrenceKind = iota
	VariableLocalBinding
	VariableReference
	VariableThemeConfig
)

type VariableOccurrence struct {
	Name  string
	File  string
	Range cst.TextRange
	Start SourcePosition
	End   SourcePosition
	Kind  VariableOccurrenceKind
}

type VariableCatalog struct {
	Name        string
	File        string
	Occurrences []VariableOccurrence
}

type VariableAnalysis struct {
	Bindings           []VariableOccurrence
	GlobalDeclarations []VariableOccurrence
	References         []VariableOccurrence
}

// NormalizeVariableName applies Sass's identifier equivalence for hyphens and
// underscores. Names are stored without the leading dollar sign.
func NormalizeVariableName(name string) string {
	return strings.ReplaceAll(strings.TrimPrefix(name, "$"), "_", "-")
}

// AnalyzeVariables classifies every native SCSS variable node in one CST
// traversal. Bindings deliberately remain document-wide: diagnostics prefer a
// possible false negative over reporting scoped parameters or loop variables
// while the user is editing incomplete SCSS.
func AnalyzeVariables(path string, root *cst.Node) VariableAnalysis {
	var analysis VariableAnalysis
	if root == nil {
		return analysis
	}
	for element := range root.Descendants() {
		node, ok := element.(*cst.Node)
		if !ok || node.Kind() != scsssyntax.ScssVariable {
			continue
		}
		name, rng := variableNameAndRange(node)
		if name == "" {
			continue
		}
		occurrence := VariableOccurrence{
			Name: name, File: path, Range: rng, Kind: VariableReference,
		}
		switch classifyVariable(node) {
		case variableGlobalDeclaration:
			occurrence.Kind = VariableGlobalDeclaration
			analysis.Bindings = append(analysis.Bindings, occurrence)
			analysis.GlobalDeclarations = append(
				analysis.GlobalDeclarations, occurrence,
			)
		case variableLocalBinding:
			occurrence.Kind = VariableLocalBinding
			analysis.Bindings = append(analysis.Bindings, occurrence)
		case variableIgnored:
			continue
		default:
			analysis.References = append(analysis.References, occurrence)
		}
	}
	return analysis
}

func ThemeVariables(fields []theme.ThemeConfigField) []VariableOccurrence {
	result := make([]VariableOccurrence, 0, len(fields))
	for _, field := range fields {
		if !field.Scss || NormalizeVariableName(field.Key) == "" {
			continue
		}
		line := max(field.Line-1, 0)
		result = append(result, VariableOccurrence{
			Name:  field.Key,
			File:  field.Path,
			Start: SourcePosition{Line: line},
			End:   SourcePosition{Line: line},
			Kind:  VariableThemeConfig,
		})
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].File != result[right].File {
			return result[left].File < result[right].File
		}
		if result[left].Start.Line != result[right].Start.Line {
			return result[left].Start.Line < result[right].Start.Line
		}
		return result[left].Name < result[right].Name
	})
	return result
}

type variableRole uint8

const (
	variableReference variableRole = iota
	variableGlobalDeclaration
	variableLocalBinding
	variableIgnored
)

func classifyVariable(variable *cst.Node) variableRole {
	if declaration := ancestorOfKind(
		variable, scsssyntax.ScssVariableDeclaration,
	); declaration != nil && firstVariable(declaration) == variable {
		if declaration.Parent() != nil &&
			declaration.Parent().Kind() == scsssyntax.ScssStylesheet ||
			hasImportantFlag(declaration, "global") {
			return variableGlobalDeclaration
		}
		return variableLocalBinding
	}

	atRule := ancestorOfKind(variable, scsssyntax.ScssAtRule)
	if atRule != nil && beforeAtRuleBlock(variable, atRule) {
		switch atRuleName(atRule) {
		case "mixin", "function":
			if leadingArgumentVariable(variable) {
				return variableLocalBinding
			}
		case "each":
			if directVariableBeforeIdentifier(variable, atRule, "in") {
				return variableLocalBinding
			}
		case "for":
			if firstDirectVariable(variable, atRule) {
				return variableLocalBinding
			}
		case "content":
			if functionName(variable, "using") &&
				leadingArgumentVariable(variable) {
				return variableLocalBinding
			}
		}
	}

	if isModuleVariable(variable) || leadingNamedArgumentVariable(variable) {
		return variableIgnored
	}
	return variableReference
}

func variableNameAndRange(variable *cst.Node) (string, cst.TextRange) {
	if variable == nil {
		return "", cst.TextRange{}
	}
	for token := range variable.ChildTokens() {
		if token.Kind() == scsssyntax.TkVariable {
			return strings.TrimPrefix(token.Text(), "$"), token.Range()
		}
	}
	return "", cst.TextRange{}
}

func ancestorOfKind(node *cst.Node, kind cst.Kind) *cst.Node {
	for current := node; current != nil; current = current.Parent() {
		if current.Kind() == kind {
			return current
		}
	}
	return nil
}

func firstVariable(root *cst.Node) *cst.Node {
	if root == nil {
		return nil
	}
	for element := range root.Descendants() {
		if node, ok := element.(*cst.Node); ok &&
			node.Kind() == scsssyntax.ScssVariable {
			return node
		}
	}
	return nil
}

func hasImportantFlag(root *cst.Node, name string) bool {
	previousBang := false
	for element := range root.Descendants() {
		token, ok := element.(*cst.Token)
		if !ok || token.Kind().IsTrivia() {
			continue
		}
		if previousBang && token.Kind() == scsssyntax.TkIdentifier &&
			strings.EqualFold(token.Text(), name) {
			return true
		}
		previousBang = token.Kind() == scsssyntax.TkOperator &&
			token.Text() == "!"
	}
	return false
}

func beforeAtRuleBlock(variable, atRule *cst.Node) bool {
	for child := range atRule.ChildNodes() {
		if child.Kind() == scsssyntax.ScssBlock {
			return variable.Range().Start < child.Range().Start
		}
	}
	return true
}

func atRuleName(atRule *cst.Node) string {
	if atRule == nil {
		return ""
	}
	for element := range atRule.Descendants() {
		token, ok := element.(*cst.Token)
		if !ok || token.Kind() != scsssyntax.TkAtKeyword {
			continue
		}
		return strings.ToLower(strings.TrimPrefix(token.Text(), "@"))
	}
	return ""
}

func leadingArgumentVariable(variable *cst.Node) bool {
	argument := ancestorOfKind(variable, scsssyntax.ScssArgument)
	return argument != nil && firstVariable(argument) == variable
}

func leadingNamedArgumentVariable(variable *cst.Node) bool {
	argument := ancestorOfKind(variable, scsssyntax.ScssArgument)
	if argument == nil || firstVariable(argument) != variable {
		return false
	}
	for token := range argument.ChildTokens() {
		if token.Kind() == scsssyntax.TkColon &&
			variable.Range().End <= token.Range().Start {
			return true
		}
	}
	return false
}

func directVariableBeforeIdentifier(
	variable, atRule *cst.Node,
	identifier string,
) bool {
	if variable.Parent() != atRule {
		return false
	}
	for element := range atRule.Descendants() {
		token, ok := element.(*cst.Token)
		if !ok || token.Kind() != scsssyntax.TkIdentifier ||
			!strings.EqualFold(token.Text(), identifier) {
			continue
		}
		return variable.Range().End <= token.Range().Start
	}
	return false
}

func firstDirectVariable(variable, atRule *cst.Node) bool {
	if variable.Parent() != atRule {
		return false
	}
	for child := range atRule.ChildNodes() {
		if child.Kind() == scsssyntax.ScssVariable {
			return child == variable
		}
	}
	return false
}

func functionName(variable *cst.Node, name string) bool {
	call := ancestorOfKind(variable, scsssyntax.ScssFunctionCall)
	return call != nil && strings.EqualFold(scssquery.FunctionName(call), name)
}

func isModuleVariable(variable *cst.Node) bool {
	for sibling := variable.PrevSibling(); sibling != nil; sibling = previousSibling(sibling) {
		if sibling.Kind().IsTrivia() {
			continue
		}
		token, ok := sibling.(*cst.Token)
		return ok && token.Text() == "."
	}
	return false
}

func previousSibling(element cst.Element) cst.Element {
	switch current := element.(type) {
	case *cst.Node:
		return current.PrevSibling()
	case *cst.Token:
		return current.PrevSibling()
	default:
		return nil
	}
}
