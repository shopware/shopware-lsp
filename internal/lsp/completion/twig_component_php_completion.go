package completion

import (
	"context"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/shopware/shopware-lsp/internal/php"
	phpresolver "github.com/shopware/shopware-lsp/internal/php/resolver"
)

const (
	exposeInTemplateAttribute = "Symfony\\UX\\TwigComponent\\Attribute\\ExposeInTemplate"
	preMountAttribute         = "Symfony\\UX\\TwigComponent\\Attribute\\PreMount"
	postMountAttribute        = "Symfony\\UX\\TwigComponent\\Attribute\\PostMount"
	livePropAttribute         = "Symfony\\UX\\LiveComponent\\Attribute\\LiveProp"
	liveActionAttribute       = "Symfony\\UX\\LiveComponent\\Attribute\\LiveAction"
	liveArgAttribute          = "Symfony\\UX\\LiveComponent\\Attribute\\LiveArg"
	liveListenerAttribute     = "Symfony\\UX\\LiveComponent\\Attribute\\LiveListener"
	postHydrateAttribute      = "Symfony\\UX\\LiveComponent\\Attribute\\PostHydrate"
	preDehydrateAttribute     = "Symfony\\UX\\LiveComponent\\Attribute\\PreDehydrate"
	preReRenderAttribute      = "Symfony\\UX\\LiveComponent\\Attribute\\PreReRender"
)

type TwigComponentPHPCompletionProvider struct{}

func NewTwigComponentPHPCompletionProvider() *TwigComponentPHPCompletionProvider {
	return &TwigComponentPHPCompletionProvider{}
}

func (p *TwigComponentPHPCompletionProvider) GetCompletions(
	_ context.Context,
	request *lsp.CompletionRequest,
) []protocol.CompletionItem {
	if request == nil || request.Root == nil ||
		request.LineIndex == nil ||
		!strings.EqualFold(
			filepath.Ext(request.TextDocument.URI),
			".php",
		) {
		return nil
	}
	offset := request.LineIndex.OffsetUTF16(
		uint32(request.Position.Line),
		uint32(request.Position.Character),
	)
	replace, attributeContext := phpAttributeReplacementRange(
		request,
		offset,
	)
	if !attributeContext {
		return nil
	}
	class, target := componentAttributeTarget(
		request.Root,
		request.Node,
		offset,
	)
	if class == nil || target == componentAttributeClassTarget {
		return nil
	}
	resolver := php.NewNameResolver(request.Root)
	component, live := phpComponentClassKinds(class, resolver)
	if !component {
		return nil
	}

	specs := componentAttributeSpecs(target, live)
	items := make([]protocol.CompletionItem, 0, len(specs))
	for _, spec := range specs {
		insertName, importEdit := componentAttributeImport(
			request,
			spec.fqn,
		)
		item := protocol.CompletionItem{
			Label:      spec.name,
			FilterText: spec.name,
			Kind:       int(protocol.ClassCompletion),
			Detail:     spec.detail + " • " + spec.fqn,
			TextEdit: protocol.TextEdit{
				Range:   phpCompletionRange(replace, request.LineIndex),
				NewText: insertName,
			},
		}
		item.Documentation.Kind = string(protocol.Markdown)
		item.Documentation.Value = spec.documentation
		if importEdit != nil {
			item.AdditionalTextEdits = []interface{}{*importEdit}
		}
		items = append(items, item)
	}
	sort.Slice(items, func(left, right int) bool {
		return items[left].Label < items[right].Label
	})
	return items
}

func (p *TwigComponentPHPCompletionProvider) GetTriggerCharacters() []string {
	return []string{"#"}
}

type componentAttributeTargetKind uint8

const (
	componentAttributeClassTarget componentAttributeTargetKind = iota
	componentAttributeMethodTarget
	componentAttributePropertyTarget
	componentAttributeParameterTarget
)

type componentAttributeSpec struct {
	name          string
	fqn           string
	detail        string
	documentation string
}

func componentAttributeSpecs(
	target componentAttributeTargetKind,
	live bool,
) []componentAttributeSpec {
	expose := componentAttributeSpec{
		name:   "ExposeInTemplate",
		fqn:    exposeInTemplateAttribute,
		detail: "Expose this member in the component template",
		documentation: "Exposes a property or method to Twig. " +
			"`name`, `getter`, and `destruct` can customize the mapping.",
	}
	switch target {
	case componentAttributePropertyTarget:
		result := []componentAttributeSpec{expose}
		if live {
			result = append(result, componentAttributeSpec{
				name:   "LiveProp",
				fqn:    livePropAttribute,
				detail: "Synchronize this Live Component property",
				documentation: "Marks a property as live state. Use " +
					"`writable: true` (or writable paths) to allow " +
					"browser-side updates.",
			})
		}
		return result
	case componentAttributeParameterTarget:
		if !live {
			return nil
		}
		return []componentAttributeSpec{{
			name:          "LiveArg",
			fqn:           liveArgAttribute,
			detail:        "Bind a Live Action or listener argument",
			documentation: "Maps this method parameter from a Live Action argument or emitted Live event payload.",
		}}
	case componentAttributeMethodTarget:
		result := []componentAttributeSpec{
			expose,
			{
				name:          "PostMount",
				fqn:           postMountAttribute,
				detail:        "Run after component props are mounted",
				documentation: "Lifecycle hook invoked after mounting component props. Supports `priority`.",
			},
			{
				name:          "PreMount",
				fqn:           preMountAttribute,
				detail:        "Transform props before component mount",
				documentation: "Lifecycle hook invoked before mounting component props. Supports `priority`.",
			},
		}
		if live {
			result = append(result,
				componentAttributeSpec{
					name:          "LiveAction",
					fqn:           liveActionAttribute,
					detail:        "Expose this method as a Live Action",
					documentation: "Makes a public component method callable by Live Component actions.",
				},
				componentAttributeSpec{
					name:          "LiveListener",
					fqn:           liveListenerAttribute,
					detail:        "Listen for a Live Component event",
					documentation: "Invokes this method when the configured Live Component event is emitted.",
				},
				componentAttributeSpec{
					name:          "PostHydrate",
					fqn:           postHydrateAttribute,
					detail:        "Run after Live Component hydration",
					documentation: "Lifecycle hook invoked after live state has been hydrated.",
				},
				componentAttributeSpec{
					name:          "PreDehydrate",
					fqn:           preDehydrateAttribute,
					detail:        "Run before Live Component dehydration",
					documentation: "Lifecycle hook invoked before live state is dehydrated.",
				},
				componentAttributeSpec{
					name:          "PreReRender",
					fqn:           preReRenderAttribute,
					detail:        "Run before a Live Component re-render",
					documentation: "Lifecycle hook invoked immediately before a live re-render.",
				},
			)
		}
		return result
	default:
		return nil
	}
}

func componentAttributeTarget(
	root,
	node *phpsyntax.Node,
	offset uint32,
) (*phpsyntax.Node, componentAttributeTargetKind) {
	if parameter := closestPHPNode(node, phpsyntax.PhpParameter); parameter != nil {
		return phpquery.ClassAt(parameter), componentAttributeParameterTarget
	}
	if property := closestPHPNode(
		node,
		phpsyntax.PhpPropertyDeclaration,
	); property != nil {
		return phpquery.ClassAt(property), componentAttributePropertyTarget
	}
	if method := phpquery.MethodAt(node); method != nil {
		return phpquery.ClassAt(method), componentAttributeMethodTarget
	}
	class := phpquery.ClassAt(node)
	if class == nil {
		var nearestClass *phpsyntax.Node
		for _, candidate := range phpquery.Classes(root) {
			body := phpquery.ClassBody(candidate)
			if body != nil &&
				offset >= body.Range().Start &&
				offset <= body.Range().End {
				class = candidate
				break
			}
			if candidate.Range().Start >= offset &&
				(nearestClass == nil ||
					candidate.Range().Start < nearestClass.Range().Start) {
				nearestClass = candidate
			}
		}
		if class == nil && nearestClass != nil {
			return nearestClass, componentAttributeClassTarget
		}
	}
	if class == nil {
		return nil, componentAttributeClassTarget
	}
	body := phpquery.ClassBody(class)
	if body == nil || offset < body.Range().Start {
		return class, componentAttributeClassTarget
	}
	var nearest *phpsyntax.Node
	for _, candidate := range append(
		phpquery.Properties(class),
		phpquery.Methods(class)...,
	) {
		if candidate.Range().Start < offset {
			continue
		}
		if nearest == nil ||
			candidate.Range().Start < nearest.Range().Start {
			nearest = candidate
		}
	}
	if nearest == nil {
		return class, componentAttributeClassTarget
	}
	if nearest.Kind() == phpsyntax.PhpPropertyDeclaration {
		return class, componentAttributePropertyTarget
	}
	return class, componentAttributeMethodTarget
}

func closestPHPNode(
	node *phpsyntax.Node,
	kind phpsyntax.Kind,
) *phpsyntax.Node {
	for current := node; current != nil; current = current.Parent() {
		if current.Kind() == kind {
			return current
		}
	}
	return nil
}

func phpComponentClassKinds(
	class *phpsyntax.Node,
	resolver *php.NameResolver,
) (component, live bool) {
	for _, attribute := range phpquery.Attributes(class) {
		name := strings.Trim(
			resolver.Resolve(phpquery.AttributeName(attribute)),
			"\\",
		)
		switch {
		case strings.EqualFold(name,
			"Symfony\\UX\\TwigComponent\\Attribute\\AsTwigComponent"):
			component = true
		case strings.EqualFold(name,
			"Symfony\\UX\\LiveComponent\\Attribute\\AsLiveComponent"):
			component = true
			live = true
		}
	}
	return component, live
}

func phpAttributeReplacementRange(
	request *lsp.CompletionRequest,
	offset uint32,
) (cst.TextRange, bool) {
	if attribute := phpquery.AttributeAt(request.Node); attribute != nil {
		for child := range attribute.ChildNodes() {
			if child.Kind() == phpsyntax.PhpName {
				nameRange := child.RangeTrimmedTrivia()
				if offset >= nameRange.Start &&
					offset <= nameRange.End {
					return nameRange, true
				}
				return cst.TextRange{}, false
			}
		}
	}
	source := string(request.DocumentContent)
	if uint64(offset) > uint64(len(source)) {
		offset = uint32(len(source))
	}
	lineStart := strings.LastIndex(source[:offset], "\n") + 1
	line := source[lineStart:offset]
	open := strings.LastIndex(line, "#[")
	if open < 0 {
		return cst.TextRange{}, false
	}
	nameStart := lineStart + open + 2
	for index := nameStart; index < int(offset); index++ {
		r := rune(source[index])
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) &&
			r != '_' && r != '\\' {
			return cst.TextRange{}, false
		}
	}
	return cst.TextRange{
		Start: uint32(nameStart),
		End:   offset,
	}, true
}

func componentAttributeImport(
	request *lsp.CompletionRequest,
	fqn string,
) (string, *protocol.TextEdit) {
	short := fqn[strings.LastIndex(fqn, "\\")+1:]
	conflict := false
	for _, declaration := range phpquery.UseDeclarations(request.Root) {
		for _, imported := range phpresolver.ParseUseDeclaration(
			declaration.Text(),
		) {
			if imported.Kind != phpresolver.ClassImport {
				continue
			}
			if strings.EqualFold(
				strings.Trim(imported.Target, "\\"),
				fqn,
			) {
				return imported.Alias, nil
			}
			if strings.EqualFold(imported.Alias, short) {
				conflict = true
			}
		}
	}
	if conflict {
		return "\\" + fqn, nil
	}
	offset := phpImportInsertionOffset(request.Root)
	edit := protocol.TextEdit{
		Range: phpCompletionRange(
			cst.TextRange{Start: offset, End: offset},
			request.LineIndex,
		),
		NewText: "\nuse " + fqn + ";",
	}
	if len(phpquery.UseDeclarations(request.Root)) == 0 {
		edit.NewText = "\n\nuse " + fqn + ";"
	}
	return short, &edit
}

func phpImportInsertionOffset(root *phpsyntax.Node) uint32 {
	var result uint32
	for _, declaration := range phpquery.UseDeclarations(root) {
		if declaration.Range().End > result {
			result = declaration.Range().End
		}
	}
	if result != 0 {
		return result
	}
	namespaces := phpquery.Nodes(root, phpsyntax.PhpNamespace)
	if len(namespaces) != 0 {
		namespace := namespaces[0]
		text := namespace.Text()
		end := strings.IndexAny(text, ";{")
		if end >= 0 {
			return namespace.Range().Start + uint32(end+1)
		}
	}
	if openTag := root.FirstToken(); openTag != nil &&
		openTag.Kind() == phpsyntax.TkOpenTag {
		return openTag.Range().End
	}
	return 0
}

func phpCompletionRange(
	rng cst.TextRange,
	lineIndex *cst.LineIndex,
) protocol.Range {
	startLine, startCharacter := lineIndex.PositionUTF16(rng.Start)
	endLine, endCharacter := lineIndex.PositionUTF16(rng.End)
	return protocol.Range{
		Start: protocol.Position{
			Line:      int(startLine),
			Character: int(startCharacter),
		},
		End: protocol.Position{
			Line:      int(endLine),
			Character: int(endCharacter),
		},
	}
}
