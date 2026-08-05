package completion

import (
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	phpresolver "github.com/shopware/shopware-lsp/internal/php/resolver"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/php/types"
	"github.com/shopware/shopware-lsp/internal/twig"
)

func (p *TwigCompletionProvider) twigStatementCompletions(
	request *lsp.CompletionRequest,
) []protocol.CompletionItem {
	if p == nil || p.phpIndex == nil || request == nil ||
		request.CompletionParams == nil || request.LineIndex == nil ||
		request.Root == nil {
		return nil
	}
	offset := request.LineIndex.OffsetUTF16(
		uint32(request.Position.Line),
		uint32(request.Position.Character),
	)
	prefix, found := twigTagCompletionPrefix(
		request.DocumentContent,
		offset,
	)
	if !found {
		return nil
	}
	ifCompletions := strings.EqualFold(prefix, "if")
	forCompletions := prefix != "" &&
		strings.HasPrefix("for", strings.ToLower(prefix))
	if !ifCompletions && !forCompletions {
		return nil
	}
	resolver := twig.PHPAccessResolver{
		PHP:  p.phpIndex,
		Twig: p.twigIndexer,
	}
	snapshot := p.phpIndex.SemanticSnapshot()
	variableTypes := p.typedTwigRootTypes(request)
	variableNames := make([]string, 0, len(variableTypes))
	for name := range variableTypes {
		variableNames = append(variableNames, name)
	}
	sort.Slice(variableNames, func(left, right int) bool {
		return strings.ToLower(variableNames[left]) <
			strings.ToLower(variableNames[right])
	})
	items := make(map[string]protocol.CompletionItem)
	for _, variableName := range variableNames {
		receiver := variableTypes[variableName]
		if receiver.IsUnknown() {
			continue
		}
		for _, member := range (phpresolver.MemberResolver{
			Snapshot: snapshot,
		}).All(receiver) {
			name, found := twigStatementMemberName(member.Symbol)
			if !found {
				continue
			}
			path := variableName + "." + name
			var label, detail string
			switch {
			case ifCompletions &&
				twigStatementBooleanType(member.Type):
				label = "if " + path
				detail = "Twig condition from typed PHP member"
			case forCompletions:
				_, element := resolver.IterableTypes(member.Type)
				if element.IsUnknown() {
					continue
				}
				label = "for " + singularTwigVariable(name) +
					" in " + path
				detail = "Twig loop from typed PHP iterable"
			default:
				continue
			}
			item := protocol.CompletionItem{
				Label:      label,
				InsertText: label,
				Kind:       int(protocol.SnippetCompletion),
				Detail:     detail,
				Deprecated: member.Symbol.Flags.Has(
					semantic.DeprecatedFlag,
				),
			}
			items[strings.ToLower(label)] = item
		}
	}
	result := make([]protocol.CompletionItem, 0, len(items))
	for _, item := range items {
		result = append(result, item)
	}
	sort.Slice(result, func(left, right int) bool {
		return strings.ToLower(result[left].Label) <
			strings.ToLower(result[right].Label)
	})
	if len(result) == 0 {
		return nil
	}
	return result
}

func (p *TwigCompletionProvider) typedTwigRootTypes(
	request *lsp.CompletionRequest,
) map[string]types.Type {
	result := twig.TwigTypeAnnotations(request.Root)
	controllerTypes := make(map[string][]types.Type)
	for _, variable := range p.templateVariables(request.TextDocument.URI) {
		if variable.Name == "" {
			continue
		}
		if _, annotated := result[variable.Name]; annotated {
			continue
		}
		value, err := types.Parse(variable.Type)
		if err == nil && !value.IsUnknown() {
			controllerTypes[variable.Name] = append(
				controllerTypes[variable.Name],
				value,
			)
		}
	}
	for name, candidates := range controllerTypes {
		switch len(candidates) {
		case 0:
			continue
		case 1:
			result[name] = candidates[0]
		default:
			result[name] = types.Union(candidates...)
		}
	}
	if p.twigIndexer != nil {
		globals, _ := p.twigIndexer.GetAllGlobals()
		for _, global := range effectiveTwigGlobals(globals) {
			if global.Name == "" {
				continue
			}
			if _, shadowed := result[global.Name]; shadowed {
				continue
			}
			value, err := types.Parse(global.Type)
			if err == nil && !value.IsUnknown() {
				result[global.Name] = value
			}
		}
	}
	return result
}

func twigStatementMemberName(
	symbol semantic.Symbol,
) (string, bool) {
	if symbol.Visibility != semantic.Public ||
		symbol.Flags.Has(semantic.StaticFlag) {
		return "", false
	}
	switch symbol.Kind {
	case semantic.PropertySymbol:
		name := strings.TrimPrefix(symbol.Name, "$")
		return name, name != ""
	case semantic.MethodSymbol:
		if strings.HasPrefix(strings.ToLower(symbol.Name), "set") ||
			strings.HasPrefix(symbol.Name, "__") {
			return "", false
		}
		for _, parameter := range symbol.Parameters {
			if !parameter.Optional &&
				!parameter.Flags.Has(semantic.VariadicFlag) {
				return "", false
			}
		}
		if attribute := twig.TwigAttributeName(symbol.Name); attribute != "" {
			return attribute, true
		}
		return symbol.Name + "()", symbol.Name != ""
	default:
		return "", false
	}
}

func twigStatementBooleanType(value types.Type) bool {
	switch value.Kind() {
	case types.BoolKind, types.TrueKind, types.FalseKind:
		return true
	case types.UnionKind, types.IntersectionKind:
		foundBoolean := false
		for _, member := range value.Arguments() {
			if member.Kind() == types.NullKind {
				continue
			}
			if !twigStatementBooleanType(member) {
				return false
			}
			foundBoolean = true
		}
		return foundBoolean
	default:
		return false
	}
}

func singularTwigVariable(name string) string {
	name = strings.TrimSuffix(name, "()")
	lower := strings.ToLower(name)
	switch {
	case strings.HasSuffix(lower, "ies") && len(name) > 3:
		return name[:len(name)-3] + "y"
	case strings.HasSuffix(lower, "sses") && len(name) > 4:
		return name[:len(name)-2]
	case strings.HasSuffix(lower, "s") &&
		!strings.HasSuffix(lower, "ss") &&
		len(name) > 1:
		return name[:len(name)-1]
	default:
		return "item"
	}
}
