package hover

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/admin"
	"github.com/shopware/shopware-lsp/internal/language"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	jsquery "github.com/shopware/shopware-lsp/internal/parser/javascript/query"
	jssyntax "github.com/shopware/shopware-lsp/internal/parser/javascript/syntax"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

// AdminHoverProvider provides hover information for Shopware Admin Vue components
type AdminHoverProvider struct {
	adminIndexer *admin.AdminComponentIndexer
	projectRoot  string
}

// NewAdminHoverProvider creates a new admin hover provider
func NewAdminHoverProvider(projectRoot string, adminIndexer *admin.AdminComponentIndexer) *AdminHoverProvider {
	return &AdminHoverProvider{
		adminIndexer: adminIndexer,
		projectRoot:  projectRoot,
	}
}

// GetHover returns hover information for Vue components
func (p *AdminHoverProvider) GetHover(ctx context.Context, params *lsp.HoverRequest) (*protocol.Hover, error) {
	ext := strings.ToLower(filepath.Ext(params.TextDocument.URI))
	languageAtCursor := lsp.EffectiveSyntaxLanguage(params.Language, params.Node)

	// Handle JS/TS files
	if ext == ".js" || ext == ".ts" ||
		ext == ".vue" && languageAtCursor == language.JavaScript {
		if params.Node == nil {
			return nil, nil
		}
		return p.jsHover(ctx, params)
	}

	// Handle Twig files (admin templates)
	if ext == ".twig" || ext == ".vue" && languageAtCursor == language.Twig {
		if params.Node == nil {
			return nil, nil
		}
		// Only process Twig files in administration directory
		if strings.Contains(params.TextDocument.URI, "Resources/app/administration") {
			return p.twigHover(ctx, params)
		}
	}

	return nil, nil
}

// twigHover handles hover for Vue components in Twig templates
func (p *AdminHoverProvider) twigHover(_ context.Context, params *lsp.HoverRequest) (*protocol.Hover, error) {
	node := params.Node
	templatePath := adminHoverTemplatePath(params.TextDocument.URI)
	liveOwner, _ := p.adminIndexer.GetComponentForDocument(
		templatePath, params.Root, string(params.DocumentContent), params.LineIndex,
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
		templatePath, params.Root, string(params.DocumentContent), params.LineIndex,
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

func (p *AdminHoverProvider) componentEventHover(
	attribute *twigsyntax.Node,
	templatePath string,
	owners ...*admin.VueComponent,
) (*protocol.Hover, error) {
	eventName := admin.NormalizeEventName(
		twigquery.HTMLAttributeName(attribute),
	)
	startTag := twigquery.StartingHTMLTagAt(attribute)
	components, err := p.componentsForMarkupTag(
		startTag, templatePath, owners...,
	)
	if err != nil {
		return nil, err
	}
	var sections []string
	for _, component := range components {
		event, found := component.ComponentEvent(eventName)
		if !found {
			continue
		}
		value := "**event** `" + admin.CanonicalEventName(event.Name) + "`"
		if event.Type != "" {
			value += ": `" + event.Type + "`"
		}
		value += "\n\nEmitted by Administration component `" + component.Name + "`."
		if documentation := strings.TrimSpace(event.Documentation); documentation != "" {
			value += "\n\n" + documentation
		}
		if event.FilePath != "" {
			path := p.makeRelativePath(event.FilePath)
			if event.Line > 0 {
				value += fmt.Sprintf("\n\nDefined in `%s:%d`.", path, event.Line)
			} else {
				value += fmt.Sprintf("\n\nDefined in `%s`.", path)
			}
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

func (p *AdminHoverProvider) componentSlotHover(
	attribute *twigsyntax.Node,
	templatePath string,
	owners ...*admin.VueComponent,
) (*protocol.Hover, error) {
	attributeName := twigquery.HTMLAttributeName(attribute)
	slotName := admin.NormalizeSlotName(attributeName)
	startTag := twigquery.StartingHTMLTagAt(attribute)
	if slotName == "" || startTag == nil {
		return nil, nil
	}
	components, complete, err :=
		p.adminIndexer.ResolveTwigSlotConsumerComponents(
			templatePath, startTag, owners...,
		)
	if err != nil || !complete {
		return nil, err
	}
	sections := make([]string, 0, len(components))
	seen := make(map[string]bool)
	for _, component := range components {
		slot, found := component.ComponentSlot(slotName)
		if !found {
			continue
		}
		key := component.Name + "\x00" + slot.FilePath + "\x00" + slot.Name
		if seen[key] {
			continue
		}
		seen[key] = true
		value := "**slot** `" + slotName + "`\n\nProvided by Administration component `" + component.Name + "`."
		if slot.IsDynamicName() {
			value += "\n\nDynamic slot family: `" + slot.DisplayName() + "`."
		}
		if len(slot.Members) > 0 {
			value += "\n\nScoped payload:"
			for _, member := range slot.Members {
				value += "\n\n- `" + member.Name + "`"
				if member.Type != "" {
					value += ": `" + member.Type + "`"
				}
			}
		} else if slot.PayloadType != "" {
			value += "\n\nScoped payload: `" + slot.PayloadType + "`"
		}
		if slot.FilePath != "" {
			value += fmt.Sprintf(
				"\n\nDefined in `%s:%d`.",
				p.makeRelativePath(slot.FilePath), slot.Line,
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

func (p *AdminHoverProvider) componentPropHover(
	attribute *twigsyntax.Node,
	templatePath string,
	owners ...*admin.VueComponent,
) (*protocol.Hover, error) {
	name := admin.NormalizePropName(twigquery.HTMLAttributeName(attribute))
	if name == "" {
		return nil, nil
	}
	startTag := twigquery.StartingHTMLTagAt(attribute)
	return p.componentPropHoverByName(
		startTag, name, templatePath, owners...,
	)
}

func (p *AdminHoverProvider) componentPropHoverByName(
	startTag *twigsyntax.Node,
	name,
	templatePath string,
	owners ...*admin.VueComponent,
) (*protocol.Hover, error) {
	if name == "" || startTag == nil {
		return nil, nil
	}
	components, err := p.componentsForMarkupTag(
		startTag, templatePath, owners...,
	)
	if err != nil {
		return nil, err
	}
	var sections []string
	for _, component := range components {
		for _, prop := range component.Props {
			if prop.Name != name {
				continue
			}
			sections = append(
				sections, adminPropMarkdown(component.Name, prop),
			)
		}
	}
	if len(sections) == 0 {
		return nil, nil
	}
	return &protocol.Hover{Contents: protocol.MarkupContent{
		Kind: protocol.Markdown, Value: strings.Join(sections, "\n\n---\n\n"),
	}}, nil
}

func (p *AdminHoverProvider) componentsForMarkupTag(
	startTag *twigsyntax.Node,
	templatePath string,
	owners ...*admin.VueComponent,
) ([]admin.VueComponent, error) {
	if p == nil || p.adminIndexer == nil || startTag == nil {
		return nil, nil
	}
	if selector, dynamic := admin.TwigDynamicComponentSelector(startTag); dynamic {
		_, components, complete, err :=
			p.adminIndexer.ResolveDynamicComponentContractsForOwner(
				templatePath, selector, firstHoverOwner(owners), startTag,
			)
		if err != nil || !complete {
			return nil, err
		}
		return components, nil
	}
	name, found := admin.StaticComponentNameForTag(startTag)
	if !found {
		return nil, nil
	}
	component, found, err := p.adminIndexer.GetComponentForTemplateTag(
		templatePath, name, owners...,
	)
	if err != nil || !found || component == nil {
		return nil, err
	}
	return []admin.VueComponent{*component}, nil
}

func (p *AdminHoverProvider) templateMemberHover(
	params *lsp.HoverRequest,
) (*protocol.Hover, error) {
	name := ""
	if twigquery.ClosestNodeOfKind(params.Node, twigsyntax.TwigVar) != nil {
		name = adminHoverTemplateRootName(
			params.Node,
			params.Token,
			params.DocumentContent,
		)
	} else if params.LineIndex != nil && params.HoverParams != nil {
		offset := params.LineIndex.OffsetUTF16(
			uint32(params.Position.Line), uint32(params.Position.Character),
		)
		name, _, _ = admin.ExpressionRootIdentifierAtOffset(
			params.DocumentContent, offset,
		)
	}
	if name == "" {
		return nil, nil
	}
	path, err := uriutil.Path(params.TextDocument.URI)
	if err != nil {
		return nil, nil
	}
	component, err := p.adminIndexer.GetComponentForDocument(
		path, params.Root, string(params.DocumentContent), params.LineIndex,
	)
	if err != nil || component == nil {
		return nil, err
	}
	if scopeMember, block, scoped := admin.TwigBlockScopeMemberAt(
		*component, params.Node, name,
	); scoped {
		value := "**Twig block scope** `" + scopeMember.Name + "`"
		if scopeMember.Type != "" {
			value += ": `" + scopeMember.Type + "`"
		}
		value += "\n\nProvided by Administration Twig block `" +
			block.Name + "`."
		if scopeMember.FilePath != "" {
			value += fmt.Sprintf(
				"\n\nDeclared in `%s:%d`.",
				p.makeRelativePath(scopeMember.FilePath), scopeMember.Line,
			)
		}
		return &protocol.Hover{Contents: protocol.MarkupContent{
			Kind: protocol.Markdown, Value: value,
		}}, nil
	}
	member, found := component.TemplateMember(name)
	origin := "Administration component `" + component.Name + "`"
	componentMember := found
	if !found {
		if builtin, builtinFound := admin.VueBuiltinMember(name); builtinFound {
			member = builtin
			found = true
			origin = "the Administration Vue component instance"
		} else if global, globalFound := admin.VueTemplateGlobal(name); globalFound {
			member = global
			found = true
			origin = "the JavaScript template runtime"
		}
	}
	if !found {
		return nil, nil
	}
	value := fmt.Sprintf("**%s** `%s`", member.Kind, member.Name)
	if member.Type != "" {
		value += ": `" + member.Type + "`"
	}
	value += "\n\nProvided by " + origin + "."
	if componentMember && member.Kind == admin.ComponentMemberProp {
		for _, prop := range component.Props {
			if prop.Name == member.Name {
				value = adminPropMarkdown(component.Name, prop)
				break
			}
		}
	} else if member.Deprecated != "" {
		value += "\n\n**Deprecated:** " + member.Deprecated
	}
	return &protocol.Hover{Contents: protocol.MarkupContent{
		Kind: protocol.Markdown, Value: value,
	}}, nil
}

func adminPropMarkdown(componentName string, prop admin.VueComponentProp) string {
	value := "**prop** `" + prop.Name + "`"
	if prop.Type != "" {
		value += ": `" + prop.Type + "`"
	}
	value += "\n\nComponent: `" + componentName + "`"
	if prop.Deprecated != "" {
		value += "\n\n**Deprecated:** " + prop.Deprecated
	}
	if documentation := strings.TrimSpace(prop.Documentation); documentation != "" {
		value += "\n\n" + documentation
	}
	if prop.Required {
		value += "\n\nRequired."
	}
	if prop.Default != "" {
		value += "\n\nDefault: `" + prop.Default + "`"
	}
	if values, complete := admin.VuePropAllowedValues(prop); len(values) > 0 {
		label := "Allowed values: "
		if !complete {
			label = "Known values: "
		}
		formatted := make([]string, 0, len(values))
		for _, allowed := range values {
			display := allowed
			if display == "" {
				display = "(empty)"
			}
			formatted = append(
				formatted,
				"`"+strings.ReplaceAll(display, "`", "\\`")+"`",
			)
		}
		value += "\n\n" + label + strings.Join(formatted, ", ")
	}
	return value
}

func adminHoverTemplateRootName(
	node *twigsyntax.Node,
	token *twigsyntax.Token,
	content []byte,
) string {
	if node == nil || token == nil {
		return ""
	}
	accessor := twigquery.ClosestNodeOfKind(node, twigsyntax.TwigAccessor)
	if accessor != nil {
		start := accessor.RangeTrimmedTrivia().Start
		end := token.Range().Start
		if start < end && int(end) <= len(content) &&
			strings.Contains(string(content[start:end]), ".") {
			return ""
		}
	}
	return strings.TrimSpace(token.Text())
}

func (p *AdminHoverProvider) jsHover(_ context.Context, params *lsp.HoverRequest) (*protocol.Hover, error) {
	node := params.Node
	if _, eventName, matched := admin.JavaScriptShopwareEventBusEventAt(
		node,
	); matched && eventName != "" {
		return p.shopwareEventBusEventHover(
			params.TextDocument.URI, eventName,
		)
	}
	if receiver, memberName, matched :=
		admin.JavaScriptShopwareUtilsMember(node); matched && memberName != "" {
		return p.shopwareUtilsMemberHover(
			params.TextDocument.URI, strings.Join(receiver, "."), memberName,
		)
	}
	if receiver, memberName, matched :=
		admin.JavaScriptShopwareContextMember(node); matched && memberName != "" {
		return p.shopwareContextMemberHover(
			params.TextDocument.URI, strings.Join(receiver, "."), memberName,
		)
	}
	if admin.IsApplicationContainerNameReference(node) {
		if container, found := admin.ApplicationContainerNamed(
			jsquery.StringValue(node),
		); found {
			return &protocol.Hover{Contents: protocol.MarkupContent{
				Kind: protocol.Markdown,
				Value: fmt.Sprintf(
					"**Application container** `%s`\n\n%s.\n\nType: `%s`.",
					container.Name, container.Description, container.InterfaceName,
				),
			}}, nil
		}
	}
	if containerName, memberName, matched :=
		admin.JavaScriptApplicationContainerMember(node); matched && memberName != "" {
		return p.applicationContainerMemberHover(
			params.TextDocument.URI, containerName, memberName,
		)
	}
	if storeName, memberName, matched := jsquery.StoreMember(node); matched && memberName != "" {
		return p.storeMemberHover(storeName, memberName)
	}
	if member, matched := jsquery.ThisMember(node); matched && member != "" {
		return p.thisMemberHover(params.TextDocument.URI, member)
	}
	if admin.IsServiceReference(node) {
		return p.serviceHover(jsquery.StringValue(node))
	}
	if admin.IsStoreReference(node) {
		return p.storeHover(jsquery.StringValue(node))
	}
	if admin.IsPrivilegeReference(node) {
		return p.privilegeHover(jsquery.StringValue(node))
	}
	if admin.IsJavaScriptModuleRouteReference(node) {
		return p.moduleRouteHover(jsquery.StringValue(node))
	}
	if path, err := uriutil.Path(params.TextDocument.URI); err == nil {
		if indexedTarget, indexedFound, indexedErr :=
			p.adminIndexer.JavaScriptSymbolAt(path, node); indexedErr != nil {
			return nil, indexedErr
		} else if indexedFound &&
			indexedTarget.Kind == admin.AdminSymbolDirective &&
			indexedTarget.Owner != "" {
			return p.directiveHoverTarget(indexedTarget)
		}
	}

	target, found := admin.JavaScriptSymbolAt(node)
	if !found {
		return nil, nil
	}
	switch target.Kind {
	case admin.AdminSymbolMixin:
		return p.mixinHover(target.Name)
	case admin.AdminSymbolDirective:
		return p.directiveHover(target.Name)
	case admin.AdminSymbolFilter:
		return p.filterHover(target.Name)
	case admin.AdminSymbolCMSElement:
		return p.cmsHover(admin.AdminCMSElement, target.Name)
	case admin.AdminSymbolCMSBlock:
		return p.cmsHover(admin.AdminCMSBlock, target.Name)
	case admin.AdminSymbolModule:
		return p.moduleHover(target.Name)
	case admin.AdminSymbolComponent:
		// Continue with the component lookup below.
	default:
		return nil, nil
	}

	componentName := target.Name
	if componentName == "" {
		return nil, nil
	}

	// Look up the component with its definition
	components, err := p.adminIndexer.GetComponentWithDefinition(componentName)
	if err != nil || len(components) == 0 {
		return nil, nil
	}

	// Build markdown content for the hover
	markdown := p.buildHoverContent(components)

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

func (p *AdminHoverProvider) shopwareEventBusEventHover(
	uri,
	eventName string,
) (*protocol.Hover, error) {
	path, err := uriutil.Path(uri)
	if err != nil || p == nil || p.adminIndexer == nil {
		return nil, err
	}
	event, found, err := p.adminIndexer.ResolveShopwareEventBusEvent(
		eventName, path,
	)
	if err != nil || !found {
		return nil, err
	}
	value := fmt.Sprintf("**Shopware EventBus event** `%s`", eventName)
	if event.Type != "" {
		value += "\n\nPayload: `" + event.Type + "`."
	}
	if event.DefinitionPath != "" {
		value += fmt.Sprintf(
			"\n\nDeclared in `%s:%d`.",
			p.makeRelativePath(event.DefinitionPath), event.DefinitionLine,
		)
	}
	return &protocol.Hover{Contents: protocol.MarkupContent{
		Kind: protocol.Markdown, Value: value,
	}}, nil
}

func (p *AdminHoverProvider) shopwareUtilsMemberHover(
	uri,
	receiver,
	memberName string,
) (*protocol.Hover, error) {
	path, err := uriutil.Path(uri)
	if err != nil || p == nil || p.adminIndexer == nil {
		return nil, err
	}
	shape, err := p.adminIndexer.ResolveShopwareUtils(receiver, path)
	if err != nil {
		return nil, err
	}
	for _, member := range shape.Members {
		if member.Name != memberName {
			continue
		}
		qualified := "Shopware.Utils"
		if receiver != "" {
			qualified += "." + receiver
		}
		qualified += "." + memberName
		value := fmt.Sprintf("**Shopware utility** `%s`", qualified)
		if member.Type != "" {
			value += "\n\nType: `" + member.Type + "`."
		}
		if member.DefinitionPath != "" {
			value += fmt.Sprintf(
				"\n\nExported in `%s:%d`.",
				p.makeRelativePath(member.DefinitionPath), member.DefinitionLine,
			)
		}
		return &protocol.Hover{Contents: protocol.MarkupContent{
			Kind: protocol.Markdown, Value: value,
		}}, nil
	}
	return nil, nil
}

func (p *AdminHoverProvider) shopwareContextMemberHover(
	uri,
	receiver,
	memberName string,
) (*protocol.Hover, error) {
	path, err := uriutil.Path(uri)
	if err != nil || p == nil || p.adminIndexer == nil {
		return nil, err
	}
	shape, err := p.adminIndexer.ResolveShopwareContext(receiver, path)
	if err != nil {
		return nil, err
	}
	for _, member := range shape.Members {
		if member.Name != memberName {
			continue
		}
		qualified := "Shopware.Context"
		if receiver != "" {
			qualified += "." + receiver
		}
		qualified += "." + memberName
		value := fmt.Sprintf("**Shopware context member** `%s`", qualified)
		if member.Type != "" {
			value += "\n\nType: `" + member.Type + "`."
		}
		if member.DefinitionPath != "" {
			value += fmt.Sprintf(
				"\n\nDefined in `%s:%d`.",
				p.makeRelativePath(member.DefinitionPath), member.DefinitionLine,
			)
		}
		return &protocol.Hover{Contents: protocol.MarkupContent{
			Kind: protocol.Markdown, Value: value,
		}}, nil
	}
	return nil, nil
}

func (p *AdminHoverProvider) applicationContainerMemberHover(
	uri,
	containerName,
	memberName string,
) (*protocol.Hover, error) {
	path, err := uriutil.Path(uri)
	if err != nil || p == nil || p.adminIndexer == nil {
		return nil, err
	}
	shape, err := p.adminIndexer.ResolveApplicationContainer(
		containerName, path,
	)
	if err != nil {
		return nil, err
	}
	var resolved *admin.TwigVueMember
	for index := range shape.Members {
		if shape.Members[index].Name == memberName {
			resolved = &shape.Members[index]
			break
		}
	}
	if containerName == "service" {
		service, serviceErr := p.serviceHover(memberName)
		if serviceErr != nil {
			return nil, serviceErr
		}
		if service != nil {
			if resolved != nil && resolved.Type != "" {
				service.Contents.Value += "\n\nContainer type: `" +
					resolved.Type + "`."
			}
			return service, nil
		}
	}
	if resolved == nil {
		return nil, nil
	}
	value := fmt.Sprintf(
		"**Application `%s` container member** `%s`",
		containerName, memberName,
	)
	if resolved.Type != "" {
		value += "\n\nType: `" + resolved.Type + "`."
	}
	if resolved.DefinitionPath != "" {
		value += fmt.Sprintf(
			"\n\nDefined in `%s:%d`.",
			p.makeRelativePath(resolved.DefinitionPath), resolved.DefinitionLine,
		)
	}
	return &protocol.Hover{Contents: protocol.MarkupContent{
		Kind: protocol.Markdown, Value: value,
	}}, nil
}

func (p *AdminHoverProvider) serviceHover(name string) (*protocol.Hover, error) {
	services, err := p.adminIndexer.GetService(name)
	if err != nil || len(services) == 0 {
		return nil, err
	}
	var sections []string
	for _, service := range services {
		value := fmt.Sprintf(
			"**Administration service** `%s`\n\nRegistered in `%s:%d`.",
			service.Name,
			p.makeRelativePath(service.FilePath),
			service.Line,
		)
		if service.ImplementationPath != "" {
			value += "\n\nImplementation: `" +
				p.makeRelativePath(service.ImplementationPath) + "`."
		}
		sections = append(sections, value)
	}
	return &protocol.Hover{Contents: protocol.MarkupContent{
		Kind: protocol.Markdown, Value: strings.Join(sections, "\n\n---\n\n"),
	}}, nil
}

func (p *AdminHoverProvider) storeHover(name string) (*protocol.Hover, error) {
	stores, err := p.adminIndexer.GetStore(name)
	if err != nil || len(stores) == 0 {
		return nil, err
	}
	var sections []string
	for _, store := range stores {
		value := fmt.Sprintf(
			"**Administration store** `%s`\n\nRegistered in `%s:%d`.",
			store.Name,
			p.makeRelativePath(store.FilePath),
			store.Line,
		)
		if len(store.Members) > 0 {
			value += fmt.Sprintf("\n\nIndexed members: %d.", len(store.Members))
		}
		sections = append(sections, value)
	}
	return &protocol.Hover{Contents: protocol.MarkupContent{
		Kind: protocol.Markdown, Value: strings.Join(sections, "\n\n---\n\n"),
	}}, nil
}

func (p *AdminHoverProvider) privilegeHover(name string) (*protocol.Hover, error) {
	privileges, err := p.adminIndexer.GetPrivilege(name)
	if err != nil || len(privileges) == 0 {
		return nil, err
	}
	sections := make([]string, 0, len(privileges))
	for _, privilege := range privileges {
		if privilege.IsBuiltin() {
			sections = append(sections, fmt.Sprintf(
				"**Built-in Administration privilege** `%s`\n\n"+
					"Provided by Shopware for administrator-only access.",
				privilege.Name,
			))
			continue
		}
		kind := "Administration privilege role"
		owner := privilege.MappingKey
		if privilege.Kind == admin.AdminPrivilegePermission {
			kind = "Administration permission"
			if privilege.Role != "" {
				owner += "." + privilege.Role
			}
		}
		value := fmt.Sprintf("**%s** `%s`", kind, privilege.Name)
		if owner != "" {
			value += "\n\nDeclared by `" + owner + "`."
		}
		value += fmt.Sprintf(
			"\n\nDefined in `%s:%d`.",
			p.makeRelativePath(privilege.FilePath), privilege.Line,
		)
		sections = append(sections, value)
	}
	return &protocol.Hover{Contents: protocol.MarkupContent{
		Kind: protocol.Markdown, Value: strings.Join(sections, "\n\n---\n\n"),
	}}, nil
}

func (p *AdminHoverProvider) moduleRouteHover(
	name string,
) (*protocol.Hover, error) {
	module, route, err := p.adminIndexer.GetModuleRoute(name)
	if err != nil || module == nil || route == nil {
		return nil, err
	}
	value := fmt.Sprintf(
		"**Administration module route** `%s`\n\nModule: `%s`",
		route.Name,
		module.Name,
	)
	if route.Path != "" {
		value += "\n\nPath: `" + route.Path + "`"
	}
	if route.Component != "" {
		value += "\n\nComponent: `" + route.Component + "`"
	}
	value += fmt.Sprintf(
		"\n\nDefined in `%s:%d`.",
		p.makeRelativePath(module.FilePath),
		route.Line,
	)
	return &protocol.Hover{Contents: protocol.MarkupContent{
		Kind: protocol.Markdown, Value: value,
	}}, nil
}

func (p *AdminHoverProvider) mixinHover(name string) (*protocol.Hover, error) {
	mixins, err := p.adminIndexer.GetMixin(name)
	if err != nil || len(mixins) == 0 {
		return nil, err
	}
	sections := make([]string, 0, len(mixins))
	for _, mixin := range mixins {
		value := fmt.Sprintf(
			"**Administration mixin** `%s`\n\nDefined in `%s:%d`.",
			mixin.Name,
			p.makeRelativePath(mixin.FilePath),
			mixin.Line,
		)
		memberCount := len(mixin.Definition.Members)
		if memberCount == 0 {
			memberCount = len(mixin.Definition.Props) +
				len(mixin.Definition.Data) +
				len(mixin.Definition.Computed) +
				len(mixin.Definition.Methods) +
				len(mixin.Definition.Injected)
		}
		if memberCount > 0 {
			value += fmt.Sprintf("\n\nIndexed members: %d.", memberCount)
		}
		sections = append(sections, value)
	}
	return &protocol.Hover{Contents: protocol.MarkupContent{
		Kind: protocol.Markdown, Value: strings.Join(sections, "\n\n---\n\n"),
	}}, nil
}

func (p *AdminHoverProvider) directiveHover(
	name string,
) (*protocol.Hover, error) {
	directives, err := p.adminIndexer.GetDirective(name)
	if err != nil || len(directives) == 0 {
		return nil, err
	}
	sections := make([]string, 0, len(directives))
	for _, directive := range directives {
		sections = append(sections, fmt.Sprintf(
			"**Administration Vue directive** `v-%s`\n\nDefined in `%s:%d`.",
			directive.Name,
			p.makeRelativePath(directive.FilePath),
			directive.Line,
		))
	}
	return &protocol.Hover{Contents: protocol.MarkupContent{
		Kind: protocol.Markdown, Value: strings.Join(sections, "\n\n---\n\n"),
	}}, nil
}

func (p *AdminHoverProvider) filterHover(
	name string,
) (*protocol.Hover, error) {
	filters, err := p.adminIndexer.GetFilter(name)
	if err != nil || len(filters) == 0 {
		return nil, err
	}
	sections := make([]string, 0, len(filters))
	for _, filter := range filters {
		value := fmt.Sprintf(
			"**Administration filter** `%s`", filter.Name,
		)
		if filter.Signature != "" {
			value += "\n\n```typescript\n" + filter.Signature + "\n```"
		}
		value += fmt.Sprintf(
			"\n\nDefined in `%s:%d`.",
			p.makeRelativePath(filter.FilePath), filter.Line,
		)
		sections = append(sections, value)
	}
	return &protocol.Hover{Contents: protocol.MarkupContent{
		Kind: protocol.Markdown, Value: strings.Join(sections, "\n\n---\n\n"),
	}}, nil
}

func (p *AdminHoverProvider) cmsHover(
	kind admin.AdminCMSRegistrationKind,
	name string,
) (*protocol.Hover, error) {
	registrations, err := p.adminIndexer.GetCMSRegistration(kind, name)
	if err != nil || len(registrations) == 0 {
		return nil, err
	}
	sections := make([]string, 0, len(registrations))
	for _, registration := range registrations {
		value := fmt.Sprintf(
			"**Shopware CMS %s** `%s`", kind, registration.Name,
		)
		if registration.Label != "" {
			value += "\n\nLabel: `" + registration.Label + "`."
		}
		if registration.Category != "" {
			value += "\n\nCategory: `" + registration.Category + "`."
		}
		for _, component := range []struct {
			label string
			name  string
		}{
			{"Component", registration.Component},
			{"Configuration component", registration.ConfigComponent},
			{"Preview component", registration.PreviewComponent},
		} {
			if component.name != "" {
				value += "\n\n" + component.label + ": `" + component.name + "`."
			}
		}
		if len(registration.Slots) > 0 {
			value += fmt.Sprintf("\n\nIndexed slots: %d.", len(registration.Slots))
		}
		value += fmt.Sprintf(
			"\n\nDefined in `%s:%d`.",
			p.makeRelativePath(registration.FilePath), registration.Line,
		)
		sections = append(sections, value)
	}
	return &protocol.Hover{Contents: protocol.MarkupContent{
		Kind: protocol.Markdown, Value: strings.Join(sections, "\n\n---\n\n"),
	}}, nil
}

func (p *AdminHoverProvider) directiveHoverForTemplate(
	name,
	templatePath string,
) (*protocol.Hover, error) {
	directives, err := p.adminIndexer.GetDirectiveForTemplate(templatePath, name)
	if err != nil || len(directives) == 0 {
		return nil, err
	}
	sections := make([]string, 0, len(directives))
	for _, directive := range directives {
		kind := "Administration Vue directive"
		if directive.Local {
			kind = "Component-local Administration Vue directive"
		}
		sections = append(sections, fmt.Sprintf(
			"**%s** `v-%s`\n\nDefined in `%s:%d`.",
			kind, directive.Name,
			p.makeRelativePath(directive.FilePath), directive.Line,
		))
	}
	return &protocol.Hover{Contents: protocol.MarkupContent{
		Kind: protocol.Markdown, Value: strings.Join(sections, "\n\n---\n\n"),
	}}, nil
}

func (p *AdminHoverProvider) directiveHoverTarget(
	target admin.AdminSymbolTarget,
) (*protocol.Hover, error) {
	if target.Owner == "" {
		return p.directiveHover(target.Name)
	}
	components, err := p.adminIndexer.GetComponentsByDefinitionPath(target.Owner)
	if err != nil {
		return nil, err
	}
	for _, component := range components {
		if local, found := component.LocalDirective(target.Name); found {
			return &protocol.Hover{Contents: protocol.MarkupContent{
				Kind: protocol.Markdown,
				Value: fmt.Sprintf(
					"**Component-local Administration Vue directive** `v-%s`\n\nDefined in `%s:%d`.",
					local.Name, p.makeRelativePath(local.FilePath), local.Line,
				),
			}}, nil
		}
	}
	return nil, nil
}

func (p *AdminHoverProvider) moduleHover(name string) (*protocol.Hover, error) {
	modules, err := p.adminIndexer.GetModule(name)
	if err != nil || len(modules) == 0 {
		return nil, err
	}
	sections := make([]string, 0, len(modules))
	for _, module := range modules {
		value := fmt.Sprintf("**Administration module** `%s`", module.Name)
		if module.Title != "" {
			value += "\n\nTitle: `" + module.Title + "`."
		}
		if module.Type != "" {
			value += "\n\nType: `" + module.Type + "`."
		}
		if module.DisplayName != "" {
			value += "\n\nName: `" + module.DisplayName + "`."
		}
		value += fmt.Sprintf("\n\nIndexed routes: %d.", len(module.Routes))
		value += fmt.Sprintf(
			"\n\nDefined in `%s:%d`.",
			p.makeRelativePath(module.FilePath), module.Line,
		)
		sections = append(sections, value)
	}
	return &protocol.Hover{Contents: protocol.MarkupContent{
		Kind: protocol.Markdown, Value: strings.Join(sections, "\n\n---\n\n"),
	}}, nil
}

func (p *AdminHoverProvider) storeMemberHover(
	storeName,
	memberName string,
) (*protocol.Hover, error) {
	stores, err := p.adminIndexer.GetStore(storeName)
	if err != nil {
		return nil, err
	}
	var sections []string
	for _, store := range stores {
		member, found := store.Member(memberName)
		if !found {
			continue
		}
		value := fmt.Sprintf("**%s** `%s`", member.Kind, member.Name)
		if member.Type != "" {
			value += ": `" + member.Type + "`"
		}
		value += fmt.Sprintf(
			"\n\nMember of Administration store `%s`.\n\nDefined in `%s:%d`.",
			store.Name,
			p.makeRelativePath(member.FilePath),
			member.Line,
		)
		sections = append(sections, value)
	}
	if len(sections) == 0 {
		return nil, nil
	}
	return &protocol.Hover{Contents: protocol.MarkupContent{
		Kind: protocol.Markdown, Value: strings.Join(sections, "\n\n---\n\n"),
	}}, nil
}

func (p *AdminHoverProvider) thisMemberHover(
	uri,
	name string,
) (*protocol.Hover, error) {
	path, err := uriutil.Path(uri)
	if err != nil {
		return nil, nil
	}
	components, err := p.adminIndexer.GetComponentsByDefinitionPath(path)
	if err != nil || len(components) == 0 {
		return nil, err
	}
	var sections []string
	seen := make(map[string]bool)
	for _, component := range components {
		member, found := component.TemplateMember(name)
		if !found {
			continue
		}
		key := component.Name + "\x00" + string(member.Kind) + "\x00" + member.Name
		if seen[key] {
			continue
		}
		seen[key] = true
		value := fmt.Sprintf("**%s** `%s`", member.Kind, member.Name)
		if member.Type != "" {
			value += ": `" + member.Type + "`"
		}
		value += "\n\nVue instance member of `" + component.Name + "`."
		if member.Deprecated != "" {
			value += "\n\n**Deprecated:** " + member.Deprecated
		} else if member.Kind == admin.ComponentMemberProp {
			if prop, propFound := component.ComponentProp(member.Name); propFound &&
				prop.Deprecated != "" {
				value += "\n\n**Deprecated:** " + prop.Deprecated
			}
		}
		if member.FilePath != "" {
			value += "\n\nDefined in `" + p.makeRelativePath(member.FilePath) + "`."
		}
		sections = append(sections, value)
	}
	if len(sections) == 0 {
		for _, builtin := range admin.VueBuiltinMembers() {
			if builtin.Name == name {
				return &protocol.Hover{Contents: protocol.MarkupContent{
					Kind:  protocol.Markdown,
					Value: "**Vue instance member** `" + name + "`",
				}}, nil
			}
		}
		return nil, nil
	}
	return &protocol.Hover{Contents: protocol.MarkupContent{
		Kind: protocol.Markdown, Value: strings.Join(sections, "\n\n---\n\n"),
	}}, nil
}

func (p *AdminHoverProvider) isInComponentCall(node *jssyntax.Node) bool {
	target, found := admin.JavaScriptSymbolAt(node)
	return found && target.Kind == admin.AdminSymbolComponent
}

func (p *AdminHoverProvider) extractComponentName(node *jssyntax.Node) string {
	target, found := admin.JavaScriptSymbolAt(node)
	if !found || target.Kind != admin.AdminSymbolComponent {
		return ""
	}
	return target.Name
}

// buildHoverContent creates the markdown content for the hover popup
func (p *AdminHoverProvider) buildHoverContent(components []admin.VueComponent) string {
	var sb strings.Builder

	for i, comp := range components {
		if i > 0 {
			sb.WriteString("\n---\n\n")
		}

		// Component name header
		fmt.Fprintf(&sb, "## `%s`\n\n", comp.Name)
		if comp.Deprecated != "" {
			fmt.Fprintf(&sb, "**Deprecated:** %s\n\n", comp.Deprecated)
		}

		// Show if it extends another component
		if comp.ExtendsComponent != "" {
			fmt.Fprintf(&sb, "**Extends**: `%s`\n\n", comp.ExtendsComponent)
		}

		// Props section
		if len(comp.Props) > 0 {
			sb.WriteString("### Props\n\n")
			for _, prop := range comp.Props {
				propLine := fmt.Sprintf("- `%s`", prop.Name)
				if prop.Type != "" {
					propLine += fmt.Sprintf(": **%s**", prop.Type)
				}
				if prop.Required {
					propLine += " *(required)*"
				}
				if prop.Deprecated != "" {
					propLine += " *(deprecated)*"
				}
				if prop.Default != "" {
					propLine += fmt.Sprintf(" = `%s`", prop.Default)
				}
				sb.WriteString(propLine + "\n")
			}
			sb.WriteString("\n")
		}

		// Emits section
		if events := comp.ComponentEvents(); len(events) > 0 {
			sb.WriteString("### Events\n\n")
			for _, event := range events {
				fmt.Fprintf(&sb, "- `%s`", admin.CanonicalEventName(event.Name))
				if event.Type != "" {
					fmt.Fprintf(&sb, ": `%s`", event.Type)
				}
				sb.WriteString("\n")
			}
			sb.WriteString("\n")
		}

		// Methods section
		if len(comp.Methods) > 0 {
			sb.WriteString("### Methods\n\n")
			for _, method := range comp.Methods {
				fmt.Fprintf(&sb, "- `%s()`\n", method)
			}
			sb.WriteString("\n")
		}

		// Computed section
		if len(comp.Computed) > 0 {
			sb.WriteString("### Computed\n\n")
			for _, computed := range comp.Computed {
				fmt.Fprintf(&sb, "- `%s`\n", computed)
			}
			sb.WriteString("\n")
		}

		if len(comp.Data) > 0 {
			sb.WriteString("### Data\n\n")
			for _, data := range comp.Data {
				fmt.Fprintf(&sb, "- `%s`\n", data)
			}
			sb.WriteString("\n")
		}

		if len(comp.Injected) > 0 {
			sb.WriteString("### Injected services\n\n")
			for _, service := range comp.Injected {
				fmt.Fprintf(&sb, "- `%s`\n", service)
			}
			sb.WriteString("\n")
		}

		// Slots section
		if len(comp.Slots) > 0 {
			sb.WriteString("### Slots\n\n")
			for _, slot := range comp.Slots {
				fmt.Fprintf(&sb, "- `%s`\n", slot.DisplayName())
			}
			sb.WriteString("\n")
		}

		// File path (relative to project root)
		if comp.DefinitionPath != "" {
			displayPath := p.makeRelativePath(comp.DefinitionPath)
			fmt.Fprintf(&sb, "*Defined in*: `%s`\n", displayPath)
		} else if comp.FilePath != "" {
			displayPath := p.makeRelativePath(comp.FilePath)
			fmt.Fprintf(&sb, "*Registered in*: `%s`\n", displayPath)
		}
	}

	return sb.String()
}

func firstHoverOwner(values []*admin.VueComponent) *admin.VueComponent {
	if len(values) == 0 {
		return nil
	}
	return values[0]
}

// makeRelativePath converts an absolute path to a path relative to the project root
func (p *AdminHoverProvider) makeRelativePath(absPath string) string {
	if p.projectRoot == "" {
		return absPath
	}
	relPath, err := filepath.Rel(p.projectRoot, absPath)
	if err != nil {
		return absPath
	}
	return relPath
}
