package hover

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/admin"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
)

// twigHover handles hover for Vue components in Twig templates
func (p *AdminHoverProvider) twigHover(_ context.Context, params *lsp.HoverRequest) (*protocol.Hover, error) {
	node := params.Node
	templatePath := adminHoverTemplatePath(params.TextDocument.URI)
	liveOwner, _ := p.adminIndexer.GetComponentForDocument(
		templatePath, params.Root, params.SourceString(), params.LineIndex,
	)
	if blockName, found := admin.TwigBlockNameAt(node, params.Token); found {
		return p.parentBlockHover(
			templatePath, blockName, params.Token.Range(), params.LineIndex,
		), nil
	}
	vueExpression := false
	if params.Root != nil && params.LineIndex != nil && params.HoverParams != nil {
		offset := params.LineIndex.OffsetUTF16(
			uint32(params.Position.Line),
			uint32(params.Position.Character),
		)
		if directive, found := admin.TwigDirectiveAtOffset(
			params.Root, offset,
		); found {
			hover, err := p.directiveHoverForTemplate(
				directive.Name, templatePath,
			)
			if hover != nil {
				hover.Range = adminHoverRange(params.LineIndex, directive.Range)
			}
			return hover, err
		}
		if reference, found := admin.TwigRegistryReferenceAtOffset(
			params.Root,
			offset,
		); found && reference.Name != "" {
			switch reference.Kind {
			case admin.AdminSymbolPrivilege:
				return p.privilegeHover(reference.Name)
			case admin.AdminSymbolModuleRoute:
				return p.moduleRouteHover(reference.Name)
			}
		}
		if candidate, found := adminDynamicComponentCandidateAt(
			params.Node, offset,
		); found {
			return p.dynamicComponentHover(
				candidate, templatePath, params.LineIndex, liveOwner,
			), nil
		}
		if startTag, field, found :=
			admin.TwigComponentObjectBindingFieldAtOffset(
				params.Root, offset,
			); found {
			return p.componentPropHoverByName(
				startTag,
				admin.NormalizePropName(field.Name),
				templatePath, liveOwner,
			)
		}
		if memberHover, handled, memberErr := p.twigVueMemberHover(
			params, offset,
		); handled || memberErr != nil {
			return memberHover, memberErr
		}
		resolvedSlot, slotErr :=
			p.adminIndexer.ResolveTwigScopedSlotBindingForOwner(
				params.Root, params.Node, params.DocumentContent, offset,
				templatePath, liveOwner,
			)
		if slotErr != nil {
			return nil, slotErr
		}
		resolvedVue, vueErr := p.adminIndexer.ResolveTwigVueBindingForComponent(
			params.Root, params.DocumentContent, offset,
			templatePath, liveOwner,
		)
		if vueErr != nil {
			return nil, vueErr
		}
		if resolvedVue != nil && (resolvedSlot == nil ||
			resolvedVue.ScopeRange.Len() <=
				resolvedSlot.Scope.TemplateRange.Len()) {
			return p.twigVueBindingHover(*resolvedVue), nil
		}
		if resolvedSlot != nil {
			return p.scopedSlotBindingHover(*resolvedSlot), nil
		}
		vueExpression = admin.IsTwigVueExpressionAt(params.Node, offset)
	}
	if twigquery.ClosestNodeOfKind(node, twigsyntax.TwigVar) != nil ||
		vueExpression {
		return p.templateMemberHover(params)
	}
	if attribute := twigquery.HTMLAttributeAt(node); attribute != nil {
		attributeName := twigquery.HTMLAttributeName(attribute)
		if _, model := admin.NormalizeModelArgument(attributeName); model {
			return p.componentModelHover(attribute, templatePath, liveOwner)
		}
		if strings.HasPrefix(attributeName, "#") ||
			strings.HasPrefix(attributeName, "v-slot:") ||
			attributeName == "v-slot" {
			return p.componentSlotHover(attribute, templatePath, liveOwner)
		}
		if admin.NormalizeEventName(attributeName) != "" {
			return p.componentEventHover(attribute, templatePath, liveOwner)
		}
		return p.componentPropHover(attribute, templatePath, liveOwner)
	}

	// Check if we're on an HTML tag name
	startTag := twigquery.StartingHTMLTagAt(node)
	if startTag == nil || params.Token == nil ||
		twigquery.HTMLTagName(startTag) != params.Token.Text() {
		return nil, nil
	}

	componentName := twigquery.HTMLTagName(startTag)
	if componentName == "" {
		return nil, nil
	}

	// Look up the component with its definition
	component, found, err := p.adminIndexer.GetComponentForTemplateTag(
		templatePath, componentName, liveOwner,
	)
	if err != nil || !found || component == nil {
		return nil, nil
	}

	// Build markdown content for the hover
	markdown := p.buildHoverContent([]admin.VueComponent{*component})

	return &protocol.Hover{
		Contents: protocol.MarkupContent{
			Kind:  protocol.Markdown,
			Value: markdown,
		},
		Range: &protocol.Range{
			Start: protocol.Position{
				Line:      params.Position.Line,
				Character: params.Position.Character,
			},
			End: protocol.Position{
				Line:      params.Position.Line,
				Character: params.Position.Character + len(componentName),
			},
		},
	}, nil
}

func (p *AdminHoverProvider) parentBlockHover(
	templatePath,
	blockName string,
	rangeValue cst.TextRange,
	lineIndex *cst.LineIndex,
) *protocol.Hover {
	if p == nil || p.adminIndexer == nil || templatePath == "" || blockName == "" {
		return nil
	}
	parent, err := p.adminIndexer.GetParentComponentForTemplate(templatePath)
	if err != nil || parent == nil {
		return nil
	}
	block, found := parent.ComponentBlock(blockName)
	if !found {
		return nil
	}
	var markdown strings.Builder
	fmt.Fprintf(&markdown, "**Administration Twig block:** `%s`\n\n", block.Name)
	fmt.Fprintf(&markdown, "**Parent component:** `%s`", parent.Name)
	if block.FilePath != "" {
		fmt.Fprintf(&markdown, "\n\n**Source:** `%s:%d`", filepath.ToSlash(block.FilePath), block.Line)
	}
	if block.Deprecated != "" {
		fmt.Fprintf(&markdown, "\n\n**Deprecated:** %s", block.Deprecated)
	}
	return &protocol.Hover{
		Contents: protocol.MarkupContent{
			Kind: protocol.Markdown, Value: markdown.String(),
		},
		Range: adminHoverRange(lineIndex, rangeValue),
	}
}

func adminDynamicComponentCandidateAt(
	node *twigsyntax.Node,
	offset uint32,
) (admin.VueDynamicComponentCandidate, bool) {
	if node == nil {
		return admin.VueDynamicComponentCandidate{}, false
	}
	selector, found := admin.TwigDynamicComponentSelector(
		twigquery.StartingHTMLTagAt(node),
	)
	if !found {
		return admin.VueDynamicComponentCandidate{}, false
	}
	return selector.CandidateAt(offset)
}

func (p *AdminHoverProvider) dynamicComponentHover(
	candidate admin.VueDynamicComponentCandidate,
	templatePath string,
	lineIndex *cst.LineIndex,
	owners ...*admin.VueComponent,
) *protocol.Hover {
	component, found, err := p.adminIndexer.GetComponentForTemplateTag(
		templatePath, candidate.Name, owners...,
	)
	if err != nil || !found || component == nil {
		return nil
	}
	return &protocol.Hover{
		Contents: protocol.MarkupContent{
			Kind: protocol.Markdown,
			Value: "**Vue dynamic component selector**\n\n" +
				p.buildHoverContent([]admin.VueComponent{*component}),
		},
		Range: adminHoverRange(lineIndex, candidate.Range),
	}
}
