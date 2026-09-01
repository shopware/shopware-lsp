package symbol

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/admin"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	jsquery "github.com/shopware/shopware-lsp/internal/parser/javascript/query"
	jssyntax "github.com/shopware/shopware-lsp/internal/parser/javascript/syntax"
	twigast "github.com/shopware/shopware-lsp/internal/parser/twig/ast"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
	"github.com/shopware/shopware-lsp/internal/twig"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

// AdminDocumentSymbolProvider exposes a live, hierarchical outline for
// Administration component definitions and their Twig templates. The open CST
// is authoritative so unsaved declarations appear without a workspace reindex.
type AdminDocumentSymbolProvider struct {
	index *admin.AdminComponentIndexer
}

func NewAdminDocumentSymbolProvider(
	index *admin.AdminComponentIndexer,
) *AdminDocumentSymbolProvider {
	return &AdminDocumentSymbolProvider{index: index}
}

func (p *AdminDocumentSymbolProvider) GetDocumentSymbols(
	ctx context.Context,
	request *lsp.DocumentSymbolRequest,
) ([]protocol.DocumentSymbol, error) {
	if request == nil || request.Document == nil ||
		request.Document.SyntaxTree == nil ||
		request.Document.SyntaxTree.Root == nil {
		return nil, nil
	}
	path, err := uriutil.Path(request.Document.URI)
	if err != nil {
		return nil, err
	}
	if !isAdministrationDocumentPath(path) {
		return nil, nil
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".js", ".ts":
		definitionName, err := p.componentDefinitionName(path)
		if err != nil {
			return nil, err
		}
		result := p.scriptSymbols(
			ctx, path, definitionName, request.Document,
		)
		sortAdminDocumentSymbols(result)
		return result, nil
	case ".twig":
		result, err := p.templateSymbols(ctx, path, request.Document)
		sortAdminDocumentSymbols(result)
		return result, err
	case ".vue":
		definitionName, definitionErr := p.componentDefinitionName(path)
		if definitionErr != nil {
			return nil, definitionErr
		}
		result := p.scriptSymbols(
			ctx, path, definitionName, request.Document,
		)
		template, templateErr := p.templateSymbols(ctx, path, request.Document)
		if templateErr != nil {
			return nil, templateErr
		}
		result = append(result, template...)
		sortAdminDocumentSymbols(result)
		return result, nil
	default:
		return nil, nil
	}
}

func (p *AdminDocumentSymbolProvider) scriptSymbols(
	ctx context.Context,
	path string,
	definitionName string,
	document *lsp.TextDocument,
) []protocol.DocumentSymbol {
	root := document.SyntaxTree.Root
	lineIndex := document.LineIndex
	var result []protocol.DocumentSymbol
	coveredObjects := make(map[string]bool)
	for _, call := range jsquery.Calls(
		root,
		"Component.register",
		"Shopware.Component.register",
		"Component.extend",
		"Shopware.Component.extend",
		"Component.override",
		"Shopware.Component.override",
	) {
		if ctx.Err() != nil {
			return result
		}
		callName := jsquery.CallName(call)
		nameNode := jsquery.StringArgument(call, 0)
		name := jsquery.StringValue(nameNode)
		if name == "" {
			continue
		}
		definitionArgument := 1
		detail := "Shopware Administration component"
		if strings.HasSuffix(callName, ".extend") {
			definitionArgument = 2
			parent := jsquery.StringValue(jsquery.StringArgument(call, 1))
			detail = "extends " + parent
		} else if strings.HasSuffix(callName, ".override") {
			detail = "component override"
		}
		expression := jsquery.ArgumentExpression(call, definitionArgument)
		object := admin.ComponentDefinitionObject(expression)
		if object != nil {
			coveredObjects[textRangeKey(object.RangeTrimmedTrivia())] = true
		}
		result = append(result, protocol.DocumentSymbol{
			Name:           name,
			Detail:         detail,
			Kind:           protocol.SymbolClass,
			Deprecated:     adminDocumentJavaScriptDeprecation(call) != "",
			Range:          adminDocumentProtocolRange(call.RangeTrimmedTrivia(), lineIndex),
			SelectionRange: adminJavaScriptNameRange(nameNode, document.Text, lineIndex),
			Children:       adminComponentDefinitionSymbols(object, path, lineIndex, document.Text),
		})
	}

	for _, export := range jsquery.ExportDefaults(root) {
		if ctx.Err() != nil {
			return result
		}
		expression := jsquery.ExportDefaultExpression(export)
		object := admin.ComponentDefinitionObject(expression)
		if object == nil || coveredObjects[textRangeKey(object.RangeTrimmedTrivia())] {
			continue
		}
		selection := object.RangeTrimmedTrivia()
		selection.End = selection.Start
		result = append(result, protocol.DocumentSymbol{
			Name:           definitionName,
			Detail:         "Shopware Administration component definition",
			Kind:           protocol.SymbolClass,
			Deprecated:     admin.JavaScriptDeprecation(export) != "",
			Range:          adminDocumentProtocolRange(export.RangeTrimmedTrivia(), lineIndex),
			SelectionRange: adminDocumentProtocolRange(selection, lineIndex),
			Children:       adminComponentDefinitionSymbols(object, path, lineIndex, document.Text),
		})
	}
	if strings.EqualFold(filepath.Ext(path), ".vue") &&
		len(jsquery.ExportDefaults(root)) == 0 && p != nil && p.index != nil {
		definition, err := p.index.GetComponentDefinition(path)
		if err == nil && definition != nil {
			programs := jsquery.Nodes(root, jssyntax.JsProgram)
			if len(programs) > 0 {
				rangeValue := programs[0].RangeTrimmedTrivia()
				selection := rangeValue
				selection.End = selection.Start
				result = append(result, protocol.DocumentSymbol{
					Name:   definitionName,
					Detail: "Shopware Administration script-setup component",
					Kind:   protocol.SymbolClass,
					Range:  adminDocumentProtocolRange(rangeValue, lineIndex),
					SelectionRange: adminDocumentProtocolRange(
						selection, lineIndex,
					),
					Children: adminScriptSetupDocumentSymbols(*definition),
				})
			}
		}
	}
	return result
}

func adminScriptSetupDocumentSymbols(
	definition admin.ComponentDefinition,
) []protocol.DocumentSymbol {
	props := make(map[string]admin.VueComponentProp, len(definition.Props))
	for _, prop := range definition.Props {
		props[prop.Name] = prop
	}
	seen := make(map[string]bool)
	var result []protocol.DocumentSymbol
	for _, member := range definition.Members {
		selection, found := adminSourceProtocolRange(member.NameRange)
		if !found || seen[member.Name] {
			continue
		}
		seen[member.Name] = true
		kind := protocol.SymbolField
		detail := member.Type
		switch member.Kind {
		case admin.ComponentMemberProp:
			kind = protocol.SymbolProperty
			detail = componentPropDocumentDetail(props[member.Name])
		case admin.ComponentMemberComputed:
			kind = protocol.SymbolProperty
		case admin.ComponentMemberMethod:
			kind = protocol.SymbolMethod
		}
		result = append(result, protocol.DocumentSymbol{
			Name: member.Name, Detail: detail, Kind: kind,
			Range: selection, SelectionRange: selection,
			Deprecated: member.Deprecated != "" ||
				props[member.Name].Deprecated != "",
		})
	}
	return result
}

func (p *AdminDocumentSymbolProvider) componentDefinitionName(
	path string,
) (string, error) {
	fallback := administrationComponentNameFromPath(path)
	if p == nil || p.index == nil {
		return fallback, nil
	}
	components, err := p.index.GetComponentsByDefinitionPath(path)
	if err != nil {
		return "", err
	}
	names := make(map[string]bool)
	for _, component := range components {
		if component.Name != "" {
			names[component.Name] = true
		}
	}
	if len(names) == 1 {
		for name := range names {
			return name, nil
		}
	}
	return fallback, nil
}

func adminComponentDefinitionSymbols(
	object *jssyntax.Node,
	path string,
	lineIndex *cst.LineIndex,
	source []byte,
) []protocol.DocumentSymbol {
	definition := admin.ParseComponentObject(object, path, lineIndex)
	if definition == nil {
		return nil
	}
	props := make(map[string]admin.VueComponentProp, len(definition.Props))
	for _, prop := range definition.Props {
		props[prop.Name] = prop
	}
	seen := make(map[string]bool)
	var result []protocol.DocumentSymbol
	for _, member := range definition.Members {
		selection, ok := adminSourceProtocolRange(member.NameRange)
		if !ok {
			continue
		}
		kind := protocol.SymbolField
		detail := member.Type
		switch member.Kind {
		case admin.ComponentMemberProp:
			kind = protocol.SymbolProperty
			if prop, found := props[member.Name]; found {
				detail = componentPropDocumentDetail(prop)
			}
		case admin.ComponentMemberComputed:
			kind = protocol.SymbolProperty
		case admin.ComponentMemberMethod:
			kind = protocol.SymbolMethod
		case admin.ComponentMemberInject:
			detail = "injected service"
		}
		deprecated := member.Deprecated != ""
		if member.Kind == admin.ComponentMemberProp &&
			props[member.Name].Deprecated != "" {
			deprecated = true
		}
		if deprecated && !strings.Contains(detail, "deprecated") {
			if detail != "" {
				detail += " · "
			}
			detail += "deprecated"
		}
		key := fmt.Sprintf(
			"%s\x00%s\x00%d:%d", member.Kind, member.Name,
			selection.Start.Line, selection.Start.Character,
		)
		if seen[key] {
			continue
		}
		seen[key] = true
		declaration := adminJavaScriptDeclarationRange(
			object, selection, lineIndex,
		)
		result = append(result, protocol.DocumentSymbol{
			Name: member.Name, Detail: detail, Kind: kind,
			Range: declaration, SelectionRange: selection,
			Deprecated: deprecated,
		})
	}

	result = append(result, adminComponentEventDocumentSymbols(
		object, definition, lineIndex, source,
	)...)
	result = append(result, adminInjectedDocumentSymbols(
		object, lineIndex, source,
	)...)
	for _, local := range definition.LocalComponents {
		selection, ok := adminSourceProtocolRange(local.NameRange)
		if !ok {
			continue
		}
		result = append(result, protocol.DocumentSymbol{
			Name: local.Name, Detail: "local component", Kind: protocol.SymbolClass,
			Range:          adminJavaScriptDeclarationRange(object, selection, lineIndex),
			SelectionRange: selection,
		})
	}
	for _, directive := range definition.LocalDirectives {
		selection, ok := adminSourceProtocolRange(directive.NameRange)
		if !ok {
			continue
		}
		result = append(result, protocol.DocumentSymbol{
			Name: "v-" + directive.Name, Detail: "local Vue directive",
			Kind:           protocol.SymbolFunction,
			Range:          adminJavaScriptDeclarationRange(object, selection, lineIndex),
			SelectionRange: selection,
		})
	}
	return result
}

func componentPropDocumentDetail(prop admin.VueComponentProp) string {
	detail := prop.Type
	if detail == "" {
		detail = "prop"
	}
	if prop.Required {
		detail += " · required"
	}
	if prop.Default != "" {
		detail += " · default " + prop.Default
	}
	if prop.Deprecated != "" {
		detail += " · deprecated"
	}
	return detail
}

func adminDocumentJavaScriptDeprecation(node *jssyntax.Node) string {
	for current := node; current != nil; current = current.Parent() {
		if current.Kind() == jssyntax.JsProgram {
			return ""
		}
		if deprecation := admin.JavaScriptDeprecation(current); deprecation != "" {
			return deprecation
		}
	}
	return ""
}

func adminComponentEventDocumentSymbols(
	object *jssyntax.Node,
	definition *admin.ComponentDefinition,
	lineIndex *cst.LineIndex,
	source []byte,
) []protocol.DocumentSymbol {
	types := make(map[string]string, len(definition.Events))
	for _, event := range definition.Events {
		types[admin.CanonicalEventName(event.Name)] = event.Type
	}
	value := jsquery.PropertyValue(jsquery.Property(object, "emits"))
	var declarations []*jssyntax.Node
	switch {
	case value == nil:
		return nil
	case value.Kind() == jssyntax.JsArray:
		declarations = jsquery.ArrayItems(value)
	case value.Kind() == jssyntax.JsObject:
		declarations = jsquery.Properties(value)
	}
	result := make([]protocol.DocumentSymbol, 0, len(declarations))
	for _, declaration := range declarations {
		nameNode := declaration
		name := ""
		if declaration.Kind() == jssyntax.JsProperty ||
			declaration.Kind() == jssyntax.JsMethod {
			nameNode = jsquery.PropertyNameNode(declaration)
			name = jsquery.PropertyName(declaration)
		} else {
			name = jsquery.StringValue(declaration)
		}
		name = admin.CanonicalEventName(name)
		if name == "" || nameNode == nil {
			continue
		}
		detail := types[name]
		if detail == "" {
			detail = "component event"
		}
		result = append(result, protocol.DocumentSymbol{
			Name: name, Detail: detail, Kind: protocol.SymbolEvent,
			Range: adminDocumentProtocolRange(
				declaration.RangeTrimmedTrivia(), lineIndex,
			),
			SelectionRange: adminJavaScriptNameRange(nameNode, source, lineIndex),
		})
	}
	return result
}

func adminInjectedDocumentSymbols(
	object *jssyntax.Node,
	lineIndex *cst.LineIndex,
	source []byte,
) []protocol.DocumentSymbol {
	value := jsquery.PropertyValue(jsquery.Property(object, "inject"))
	var declarations []*jssyntax.Node
	switch {
	case value == nil:
		return nil
	case value.Kind() == jssyntax.JsArray:
		declarations = jsquery.ArrayItems(value)
	case value.Kind() == jssyntax.JsObject:
		declarations = jsquery.Properties(value)
	}
	result := make([]protocol.DocumentSymbol, 0, len(declarations))
	for _, declaration := range declarations {
		nameNode := declaration
		name := ""
		if declaration.Kind() == jssyntax.JsProperty ||
			declaration.Kind() == jssyntax.JsMethod {
			nameNode = jsquery.PropertyNameNode(declaration)
			name = jsquery.PropertyName(declaration)
		} else {
			name = jsquery.StringValue(declaration)
		}
		if name == "" || nameNode == nil {
			continue
		}
		result = append(result, protocol.DocumentSymbol{
			Name: name, Detail: "injected service", Kind: protocol.SymbolField,
			Range: adminDocumentProtocolRange(
				declaration.RangeTrimmedTrivia(), lineIndex,
			),
			SelectionRange: adminJavaScriptNameRange(nameNode, source, lineIndex),
		})
	}
	return result
}

func adminJavaScriptDeclarationRange(
	object *jssyntax.Node,
	selection protocol.Range,
	lineIndex *cst.LineIndex,
) protocol.Range {
	start := lineIndex.OffsetUTF16(
		uint32(selection.Start.Line), uint32(selection.Start.Character),
	)
	end := lineIndex.OffsetUTF16(
		uint32(selection.End.Line), uint32(selection.End.Character),
	)
	best := cst.TextRange{Start: start, End: end}
	found := false
	for _, declaration := range jsquery.Nodes(
		object, jssyntax.JsProperty, jssyntax.JsMethod,
	) {
		candidate := declaration.RangeTrimmedTrivia()
		if candidate.Start > start || candidate.End < end {
			continue
		}
		if found && best.End-best.Start <= candidate.End-candidate.Start {
			continue
		}
		best = candidate
		found = true
	}
	return adminDocumentProtocolRange(best, lineIndex)
}

func (p *AdminDocumentSymbolProvider) templateSymbols(
	ctx context.Context,
	path string,
	document *lsp.TextDocument,
) ([]protocol.DocumentSymbol, error) {
	name := administrationComponentNameFromTemplatePath(path)
	if p != nil && p.index != nil {
		component, err := p.index.GetComponentByTemplatePath(path)
		if err != nil {
			return nil, err
		}
		if component != nil && component.Name != "" {
			name = component.Name
		}
	}
	root := document.SyntaxTree.Root
	lineIndex := document.LineIndex
	var blocks []protocol.DocumentSymbol
	for _, node := range twigquery.Nodes(root, twigsyntax.TwigBlock) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		block, ok := twigast.CastTwigBlock(node)
		blockName := twigquery.BlockName(node)
		if !ok || block.Name() == nil || blockName == "" {
			continue
		}
		deprecation := twig.BlockDeprecation(node, document.SourceString())
		detail := "Twig block"
		if deprecation != "" {
			detail += " · deprecated"
		}
		blocks = append(blocks, protocol.DocumentSymbol{
			Name: blockName, Detail: detail, Kind: protocol.SymbolMethod,
			Deprecated: deprecation != "",
			Range:      adminDocumentProtocolRange(node.RangeTrimmedTrivia(), lineIndex),
			SelectionRange: adminDocumentProtocolRange(
				block.Name().Range(), lineIndex,
			),
		})
	}
	var topLevelSlots []protocol.DocumentSymbol
	for _, node := range twigquery.Nodes(root, twigsyntax.HtmlStartingTag) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		tag, ok := twigast.CastHtmlStartingTag(node)
		if !ok || tag.Name() == nil || tag.Name().Text() != "slot" {
			continue
		}
		slot, known := admin.SlotDeclaration(tag, lineIndex)
		if !known || slot.DisplayName() == "" {
			continue
		}
		symbol := protocol.DocumentSymbol{
			Name: slot.DisplayName(), Detail: "Vue slot", Kind: protocol.SymbolProperty,
			Range:          adminDocumentProtocolRange(node.RangeTrimmedTrivia(), lineIndex),
			SelectionRange: adminTwigSlotNameRange(tag, lineIndex),
			Children: adminTwigSlotMemberSymbols(
				tag, lineIndex,
			),
		}
		blockIndex := smallestContainingDocumentSymbol(blocks, symbol.Range)
		if blockIndex >= 0 {
			blocks[blockIndex].Children = append(blocks[blockIndex].Children, symbol)
		} else {
			topLevelSlots = append(topLevelSlots, symbol)
		}
	}
	children := append(blocks, topLevelSlots...)
	fullRange := adminDocumentProtocolRange(
		cst.TextRange{Start: 0, End: uint32(len(document.Text))}, lineIndex,
	)
	selection := protocol.Range{Start: fullRange.Start, End: fullRange.Start}
	return []protocol.DocumentSymbol{{
		Name: name, Detail: "Shopware Administration component template",
		Kind: protocol.SymbolClass, Range: fullRange, SelectionRange: selection,
		Children: children,
	}}, nil
}

func adminTwigSlotMemberSymbols(
	tag twigast.HtmlStartingTag,
	lineIndex *cst.LineIndex,
) []protocol.DocumentSymbol {
	var result []protocol.DocumentSymbol
	seen := make(map[string]bool)
	for _, attribute := range tag.Attributes() {
		if attribute.Name() == nil {
			continue
		}
		attributeName := twigquery.HTMLAttributeName(attribute.Syntax())
		if attributeName == "name" || attributeName == ":name" ||
			attributeName == "v-bind:name" {
			continue
		}
		if attributeName == "v-bind" {
			value, ok := attribute.Value()
			if !ok {
				continue
			}
			inner, ok := value.GetInner()
			if !ok {
				continue
			}
			fields, _ := admin.VueObjectBindingFields(
				inner.Syntax().Text(), inner.Syntax().Range().Start,
			)
			for _, field := range fields {
				if field.Name == "" || seen[field.Name] {
					continue
				}
				seen[field.Name] = true
				rng := adminDocumentProtocolRange(field.NameRange, lineIndex)
				result = append(result, protocol.DocumentSymbol{
					Name: field.Name, Detail: "slot payload", Kind: protocol.SymbolField,
					Range: rng, SelectionRange: rng,
				})
			}
			continue
		}
		prop, found := admin.VuePropReferenceForAttribute(
			attributeName, attribute.Name().Range(),
		)
		if !found || prop.Name == "" || seen[prop.Name] {
			continue
		}
		seen[prop.Name] = true
		rng := adminDocumentProtocolRange(prop.Range, lineIndex)
		result = append(result, protocol.DocumentSymbol{
			Name: prop.Name, Detail: "slot payload", Kind: protocol.SymbolField,
			Range: rng, SelectionRange: rng,
		})
	}
	return result
}

func adminTwigSlotNameRange(
	tag twigast.HtmlStartingTag,
	lineIndex *cst.LineIndex,
) protocol.Range {
	for _, attribute := range tag.Attributes() {
		name := twigquery.HTMLAttributeName(attribute.Syntax())
		if name != "name" && name != ":name" && name != "v-bind:name" {
			continue
		}
		if value, ok := attribute.Value(); ok {
			if inner, innerOK := value.GetInner(); innerOK {
				return adminDocumentProtocolRange(
					inner.Syntax().RangeTrimmedTrivia(), lineIndex,
				)
			}
		}
	}
	return adminDocumentProtocolRange(tag.Name().Range(), lineIndex)
}

func smallestContainingDocumentSymbol(
	symbols []protocol.DocumentSymbol,
	rng protocol.Range,
) int {
	best := -1
	for index := range symbols {
		if !protocolRangeContains(symbols[index].Range, rng) {
			continue
		}
		if best < 0 || protocolRangeContains(symbols[best].Range, symbols[index].Range) {
			best = index
		}
	}
	return best
}

func protocolRangeContains(outer, inner protocol.Range) bool {
	return !protocolPositionLess(inner.Start, outer.Start) &&
		!protocolPositionLess(outer.End, inner.End)
}

func protocolPositionLess(left, right protocol.Position) bool {
	return left.Line < right.Line ||
		(left.Line == right.Line && left.Character < right.Character)
}

func adminJavaScriptNameRange(
	node *jssyntax.Node,
	source []byte,
	lineIndex *cst.LineIndex,
) protocol.Range {
	if node == nil {
		return protocol.Range{}
	}
	rng := node.RangeTrimmedTrivia()
	if rng.End-rng.Start >= 2 && int(rng.End) <= len(source) {
		first := source[rng.Start]
		last := source[rng.End-1]
		if (first == '\'' || first == '"' || first == '`') && last == first {
			rng.Start++
			rng.End--
		}
	}
	return adminDocumentProtocolRange(rng, lineIndex)
}

func adminSourceProtocolRange(
	rng admin.AdminSourceRange,
) (protocol.Range, bool) {
	start := protocol.Position{
		Line: rng.StartLine, Character: rng.StartCharacter,
	}
	end := protocol.Position{Line: rng.EndLine, Character: rng.EndCharacter}
	if protocolPositionLess(end, start) || start == end {
		return protocol.Range{}, false
	}
	return protocol.Range{Start: start, End: end}, true
}

func adminDocumentProtocolRange(
	rng cst.TextRange,
	lineIndex *cst.LineIndex,
) protocol.Range {
	startLine, startCharacter := lineIndex.PositionUTF16(rng.Start)
	endLine, endCharacter := lineIndex.PositionUTF16(rng.End)
	return protocol.Range{
		Start: protocol.Position{
			Line: int(startLine), Character: int(startCharacter),
		},
		End: protocol.Position{
			Line: int(endLine), Character: int(endCharacter),
		},
	}
}

func administrationComponentNameFromPath(path string) string {
	base := filepath.Base(path)
	if base == "index.js" || base == "index.ts" {
		return filepath.Base(filepath.Dir(path))
	}
	return strings.TrimSuffix(base, filepath.Ext(base))
}

func administrationComponentNameFromTemplatePath(path string) string {
	base := filepath.Base(path)
	return strings.TrimSuffix(base, ".html.twig")
}

func isAdministrationDocumentPath(path string) bool {
	normalized := filepath.ToSlash(path)
	return strings.Contains(normalized, "/Resources/app/administration/")
}

func textRangeKey(rng cst.TextRange) string {
	return fmt.Sprintf("%d:%d", rng.Start, rng.End)
}

func sortAdminDocumentSymbols(symbols []protocol.DocumentSymbol) {
	for index := range symbols {
		sortAdminDocumentSymbols(symbols[index].Children)
	}
	sort.SliceStable(symbols, func(left, right int) bool {
		return protocolPositionLess(
			symbols[left].Range.Start, symbols[right].Range.Start,
		)
	})
}

var _ lsp.DocumentSymbolProvider = (*AdminDocumentSymbolProvider)(nil)
