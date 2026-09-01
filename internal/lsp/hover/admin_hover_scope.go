package hover

import (
	"fmt"
	"strings"

	"github.com/shopware/shopware-lsp/internal/admin"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

func (p *AdminHoverProvider) componentModelHover(
	attribute *twigsyntax.Node,
	templatePath string,
	owners ...*admin.VueComponent,
) (*protocol.Hover, error) {
	startTag := twigquery.StartingHTMLTagAt(attribute)
	components, err := p.componentsForMarkupTag(
		startTag, templatePath, owners...,
	)
	if err != nil || len(components) == 0 {
		return nil, err
	}
	var sections []string
	for _, component := range components {
		model, found := component.ComponentModel(
			twigquery.HTMLAttributeName(attribute),
		)
		if !found {
			continue
		}
		value := "**model binding** `" + model.AttributeName + "`"
		if valueType := admin.VuePropValueType(model.Prop.Type); valueType != "" {
			value += ": `" + valueType + "`"
		}
		value += "\n\nReads prop `" + model.PropName + "` and writes through event `" +
			model.EventName + "` on Administration component `" + component.Name + "`."
		if model.Prop.Deprecated != "" {
			value += "\n\n**Deprecated:** " + model.Prop.Deprecated
		}
		if model.Prop.FilePath != "" {
			value += fmt.Sprintf(
				"\n\nProp defined in `%s:%d`.",
				p.makeRelativePath(model.Prop.FilePath), model.Prop.Line,
			)
		}
		if model.Event.FilePath != "" {
			value += fmt.Sprintf(
				"\n\nEvent defined in `%s:%d`.",
				p.makeRelativePath(model.Event.FilePath), model.Event.Line,
			)
		}
		sections = append(sections, value)
	}
	if len(sections) == 0 {
		return nil, nil
	}
	return &protocol.Hover{Contents: protocol.MarkupContent{
		Kind: protocol.Markdown, Value: strings.Join(sections, "\n\n---\n\n"),
	}}, nil
}

func (p *AdminHoverProvider) twigVueMemberHover(
	params *lsp.HoverRequest,
	offset uint32,
) (*protocol.Hover, bool, error) {
	if params == nil || params.Root == nil || params.Node == nil {
		return nil, false, nil
	}
	access, found := admin.TwigVueExpressionMemberAtOffset(
		params.Root, params.DocumentContent, offset,
	)
	if !found || access.Member == "" {
		return nil, false, nil
	}
	templatePath := adminHoverTemplatePath(params.TextDocument.URI)
	liveComponent, _ := p.adminIndexer.GetComponentForDocument(
		templatePath, params.Root, params.SourceString(), params.LineIndex,
	)
	resolvedSlot, err := p.adminIndexer.ResolveTwigScopedSlotMemberForOwner(
		params.Root, params.Node, params.DocumentContent, offset,
		templatePath, liveComponent,
	)
	if err != nil {
		return nil, true, err
	}
	resolvedVue, err := p.adminIndexer.ResolveTwigVueMemberForComponent(
		params.Root, params.DocumentContent, offset, templatePath, liveComponent,
	)
	if err != nil {
		return nil, true, err
	}
	if resolvedVue != nil && (resolvedSlot == nil ||
		resolvedVue.Binding.ScopeRange.Len() <=
			resolvedSlot.Scope.TemplateRange.Len()) {
		if !resolvedVue.ReceiverFound || !resolvedVue.MemberFound {
			return nil, true, nil
		}
		qualified := access.QualifiedName()
		value := "**property** `" + qualified + "`"
		if resolvedVue.Member.Type != "" {
			value += ": `" + resolvedVue.Member.Type + "`"
		}
		value += "\n\nResolved on the lexical `" + resolvedVue.Binding.Name +
			"` binding in this template."
		if resolvedVue.ReceiverType != "" {
			label := "Receiver type"
			if len(access.Receiver) == 0 {
				label = "Binding type"
			}
			value += "\n\n" + label + ": `" +
				resolvedVue.ReceiverType + "`."
		}
		if resolvedVue.Binding.Iterable != "" {
			value += "\n\nIterates `" + resolvedVue.Binding.Iterable + "`."
		}
		return &protocol.Hover{
			Contents: protocol.MarkupContent{
				Kind: protocol.Markdown, Value: value,
			},
			Range: adminHoverRange(params.LineIndex, access.MemberRange),
		}, true, nil
	}
	if resolvedSlot != nil {
		if !resolvedSlot.MemberFound {
			return nil, true, nil
		}
		value := "**slot prop** `" +
			resolvedSlot.Access.QualifiedName() + "`"
		if resolvedSlot.Member.Type != "" {
			value += ": `" + resolvedSlot.Member.Type + "`"
		}
		value += "\n\nExposed by scoped slot `" +
			resolvedSlot.QualifiedName() + "`."
		value += scopedSlotCandidateMarkdown(
			resolvedSlot.ResolvedTwigScopedSlot,
		)
		value += p.scopedSlotMemberDeclarationMarkdown(
			resolvedSlot.Members,
		)
		return &protocol.Hover{
			Contents: protocol.MarkupContent{
				Kind: protocol.Markdown, Value: value,
			},
			Range: adminHoverRange(params.LineIndex, access.MemberRange),
		}, true, nil
	}
	resolvedInstance, err :=
		p.adminIndexer.ResolveTwigVueInstanceMemberForComponent(
			params.Root, params.DocumentContent, offset,
			templatePath, liveComponent,
		)
	if err != nil {
		return nil, true, err
	}
	if resolvedInstance != nil {
		if !resolvedInstance.ReceiverFound || !resolvedInstance.MemberFound {
			return nil, true, nil
		}
		value := "**property** `" + resolvedInstance.QualifiedName() + "`"
		if resolvedInstance.Member.Type != "" {
			value += ": `" + resolvedInstance.Member.Type + "`"
		}
		value += "\n\nResolved through **" +
			string(resolvedInstance.RootMember.Kind) + "** `" +
			resolvedInstance.RootMember.Name + "` on Administration component `" +
			resolvedInstance.Component.Name + "`."
		if resolvedInstance.ReceiverType != "" {
			value += "\n\nReceiver type: `" +
				resolvedInstance.ReceiverType + "`."
		}
		return &protocol.Hover{
			Contents: protocol.MarkupContent{
				Kind: protocol.Markdown, Value: value,
			},
			Range: adminHoverRange(params.LineIndex, access.MemberRange),
		}, true, nil
	}
	// The expression is a direct member access, but its receiver has no shape
	// known to the Administration frontend. Do not misreport it as a component
	// instance member with the same property name.
	return nil, true, nil
}

func adminHoverRange(
	index *cst.LineIndex,
	rangeValue cst.TextRange,
) *protocol.Range {
	if index == nil || rangeValue.Len() == 0 {
		return nil
	}
	startLine, startCharacter := index.PositionUTF16(rangeValue.Start)
	endLine, endCharacter := index.PositionUTF16(rangeValue.End)
	return &protocol.Range{
		Start: protocol.Position{
			Line: int(startLine), Character: int(startCharacter),
		},
		End: protocol.Position{
			Line: int(endLine), Character: int(endCharacter),
		},
	}
}

func adminHoverTemplatePath(uri string) string {
	path, err := uriutil.Path(uri)
	if err != nil {
		return ""
	}
	return path
}

func (p *AdminHoverProvider) twigVueBindingHover(
	binding admin.TwigVueBinding,
) *protocol.Hover {
	kind := "v-for local"
	value := "**" + kind + "** `" + binding.Name + "`"
	if binding.Kind == admin.TwigVueBindingEvent {
		kind = "event payload"
		value = "**" + kind + "** `" + binding.Name + "`"
	}
	if binding.Type != "" {
		value += ": `" + binding.Type + "`"
	}
	if binding.Kind == admin.TwigVueBindingFor && binding.Iterable != "" {
		value += "\n\nIterates `" + binding.Iterable + "`."
	}
	if binding.Kind == admin.TwigVueBindingEvent {
		if binding.ComponentName != "" && binding.EventName != "" {
			value += "\n\nEvent: `" + binding.ComponentName + "." +
				binding.EventName + "`."
		}
		if binding.DefinitionPath != "" {
			path := p.makeRelativePath(binding.DefinitionPath)
			if binding.DefinitionLine > 0 {
				value += fmt.Sprintf(
					"\n\nDeclared in `%s:%d`.", path, binding.DefinitionLine,
				)
			} else {
				value += "\n\nDeclared in `" + path + "`."
			}
		}
	}
	return &protocol.Hover{Contents: protocol.MarkupContent{
		Kind: protocol.Markdown, Value: value,
	}}
}

func (p *AdminHoverProvider) scopedSlotBindingHover(
	resolved admin.ResolvedTwigSlotBinding,
) *protocol.Hover {
	kind := "slot prop"
	typeName := resolved.Member.Type
	contractName := resolved.Binding.MemberName
	if resolved.Binding.WholeObject {
		kind = "slot payload"
		typeName = resolved.Slot.PayloadType
		contractName = ""
	}
	value := "**" + kind + "** `" + resolved.Identifier + "`"
	if typeName != "" {
		value += ": `" + typeName + "`"
	}
	value += "\n\nScoped slot: `" + resolved.QualifiedName() + "`"
	value += scopedSlotCandidateMarkdown(resolved.ResolvedTwigScopedSlot)
	if resolved.Slot.IsDynamicName() {
		value += "\n\nDynamic slot family: `" +
			resolved.Slot.DisplayName() + "`."
	}
	if contractName != "" && resolved.Identifier != contractName {
		value += "\n\nContract member: `" + contractName + "`."
	}
	declarations := resolved.Members
	if resolved.Binding.WholeObject {
		declarations = nil
		for _, contract := range resolved.Contracts {
			declarations = append(declarations, admin.VueComponentSlotMember{
				Name: resolved.Scope.SlotName, Type: contract.Slot.PayloadType,
				FilePath: contract.Slot.FilePath, Line: contract.Slot.Line,
			})
		}
	}
	if declarationMarkdown := p.scopedSlotMemberDeclarationMarkdown(
		declarations,
	); declarationMarkdown != "" {
		value += declarationMarkdown
	} else {
		filePath := resolved.Slot.FilePath
		line := resolved.Slot.Line
		if resolved.MemberFound && resolved.Member.FilePath != "" {
			filePath = resolved.Member.FilePath
			line = resolved.Member.Line
		}
		if filePath == "" {
			return &protocol.Hover{Contents: protocol.MarkupContent{
				Kind: protocol.Markdown, Value: value,
			}}
		}
		if line > 0 {
			value += fmt.Sprintf(
				"\n\nDeclared in `%s:%d`.", p.makeRelativePath(filePath), line,
			)
		} else {
			value += "\n\nDeclared in `" + p.makeRelativePath(filePath) + "`."
		}
	}
	return &protocol.Hover{Contents: protocol.MarkupContent{
		Kind: protocol.Markdown, Value: value,
	}}
}

func scopedSlotCandidateMarkdown(
	resolved admin.ResolvedTwigScopedSlot,
) string {
	names := resolved.ComponentNames()
	if len(names) <= 1 {
		return ""
	}
	return "\n\nDynamic component candidates: `" +
		strings.Join(names, "`, `") + "`."
}

func (p *AdminHoverProvider) scopedSlotMemberDeclarationMarkdown(
	members []admin.VueComponentSlotMember,
) string {
	var result []string
	seen := make(map[string]bool)
	for _, member := range members {
		if member.FilePath == "" {
			continue
		}
		key := fmt.Sprintf("%s:%d", member.FilePath, member.Line)
		if seen[key] {
			continue
		}
		seen[key] = true
		path := p.makeRelativePath(member.FilePath)
		if member.Line > 0 {
			path += fmt.Sprintf(":%d", member.Line)
		}
		result = append(result, "`"+path+"`")
	}
	if len(result) == 0 {
		return ""
	}
	label := "Declared in "
	if len(result) > 1 {
		label = "Candidate declarations: "
	}
	return "\n\n" + label + strings.Join(result, ", ") + "."
}
