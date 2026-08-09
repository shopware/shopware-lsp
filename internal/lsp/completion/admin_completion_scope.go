package completion

import (
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/admin"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

func (p *AdminCompletionProvider) twigVueMemberCompletionsAt(
	params *lsp.CompletionRequest,
	slot *admin.ResolvedTwigScopedSlot,
	_ []admin.TwigVueBinding,
	offset uint32,
) ([]protocol.CompletionItem, bool) {
	if p == nil || p.adminIndexer == nil || params == nil || params.Root == nil {
		return nil, false
	}
	access, found := admin.TwigVueExpressionMemberAtOffset(
		params.Root, params.DocumentContent, offset,
	)
	if !found {
		return nil, false
	}
	slotOwnsRoot := false
	if slot != nil {
		for _, binding := range slot.Scope.Bindings {
			if binding.WholeObject && binding.LocalName == access.Root {
				slotOwnsRoot = true
				break
			}
		}
	}
	templatePath := adminTemplatePath(params.TextDocument.URI)
	liveComponent, _ := p.adminIndexer.GetComponentForDocument(
		templatePath, params.Root, string(params.DocumentContent), params.LineIndex,
	)
	resolvedVue, vueErr := p.adminIndexer.ResolveTwigVueMemberForComponent(
		params.Root, params.DocumentContent, offset, templatePath, liveComponent,
	)
	if vueErr != nil {
		return nil, true
	}
	if resolvedVue != nil && (!slotOwnsRoot || slot == nil ||
		resolvedVue.Binding.ScopeRange.Len() <= slot.Scope.TemplateRange.Len()) {
		if !resolvedVue.ReceiverFound {
			return nil, true
		}
		items := make(
			[]protocol.CompletionItem, 0, len(resolvedVue.ReceiverMembers),
		)
		for _, member := range resolvedVue.ReceiverMembers {
			if len(access.Receiver) == 0 && member.Range == access.MemberRange &&
				offset >= member.Range.End && len(
				admin.TwigVueBindingMemberReferences(
					params.Root, params.DocumentContent,
					resolvedVue.Binding, member.Name,
				),
			) == 1 {
				continue
			}
			kind := "observed member"
			if resolvedVue.MembersComplete || member.Type != "" ||
				member.DefinitionPath != "" {
				kind = "typed member"
			}
			detail := kind + " · " + resolvedVue.Binding.Name
			if resolvedVue.ReceiverType != "" {
				detail += " · " + resolvedVue.ReceiverType
			}
			if member.Type != "" {
				detail += " · " + member.Type
			}
			item := protocol.CompletionItem{
				Label: member.Name, Kind: int(protocol.PropertyCompletion),
				Detail: detail,
			}
			item.Documentation.Kind = string(protocol.Markdown)
			item.Documentation.Value = "Available on the lexical `" +
				resolvedVue.Binding.Name + "` binding in this template."
			items = append(items, item)
		}
		sortCompletionItems(items)
		return items, true
	}
	if slotOwnsRoot {
		resolvedMember, resolveErr :=
			p.adminIndexer.ResolveTwigScopedSlotMemberForOwner(
				params.Root, params.Node, params.DocumentContent, offset,
				templatePath, liveComponent,
			)
		if resolveErr != nil || resolvedMember == nil ||
			!resolvedMember.ReceiverFound {
			return nil, true
		}
		if len(access.Receiver) == 0 {
			return scopedSlotContractCompletions(*slot), true
		}
		items := make(
			[]protocol.CompletionItem, 0,
			len(resolvedMember.ReceiverMembers),
		)
		for _, member := range resolvedMember.ReceiverMembers {
			detail := "slot member · " + resolvedMember.QualifiedName()
			if resolvedMember.ReceiverType != "" {
				detail += " · " + resolvedMember.ReceiverType
			}
			if member.Type != "" {
				detail += " · " + member.Type
			}
			items = append(items, protocol.CompletionItem{
				Label: member.Name, Kind: int(protocol.PropertyCompletion),
				Detail: detail,
			})
		}
		sortCompletionItems(items)
		return items, true
	}
	resolvedInstance, instanceErr :=
		p.adminIndexer.ResolveTwigVueInstanceMemberForComponent(
			params.Root, params.DocumentContent, offset,
			templatePath, liveComponent,
		)
	if instanceErr != nil {
		return nil, true
	}
	if resolvedInstance != nil {
		if !resolvedInstance.ReceiverFound {
			return nil, true
		}
		items := make(
			[]protocol.CompletionItem, 0,
			len(resolvedInstance.ReceiverMembers),
		)
		for _, member := range resolvedInstance.ReceiverMembers {
			kind := protocol.PropertyCompletion
			if strings.Contains(member.Type, "=>") {
				kind = protocol.MethodCompletion
			}
			detail := "component member · " + resolvedInstance.Access.Root
			if resolvedInstance.ReceiverType != "" {
				detail += " · " + resolvedInstance.ReceiverType
			}
			if member.Type != "" {
				detail += " · " + member.Type
			}
			item := protocol.CompletionItem{
				Label: member.Name, Kind: int(kind), Detail: detail,
			}
			item.Documentation.Kind = string(protocol.Markdown)
			item.Documentation.Value = "Available through `" +
				resolvedInstance.QualifiedName() + "` on Administration component `" +
				resolvedInstance.Component.Name + "`."
			items = append(items, item)
		}
		sortCompletionItems(items)
		return items, true
	}
	// A direct member expression should never fall back to unrelated Vue
	// instance members. Unknown shapes intentionally produce no suggestions.
	return nil, true
}

func adminCompletionOffset(params *lsp.CompletionRequest) (uint32, bool) {
	if params == nil || params.LineIndex == nil || params.CompletionParams == nil {
		return 0, false
	}
	return params.LineIndex.OffsetUTF16(
		uint32(params.Position.Line), uint32(params.Position.Character),
	), true
}

func (p *AdminCompletionProvider) twigVueBindingsAtCompletion(
	params *lsp.CompletionRequest,
	offset uint32,
	hasOffset bool,
) []admin.TwigVueBinding {
	if p.adminIndexer == nil || params == nil || params.Root == nil || !hasOffset {
		return nil
	}
	templatePath := adminTemplatePath(params.TextDocument.URI)
	liveComponent, _ := p.adminIndexer.GetComponentForDocument(
		templatePath, params.Root, string(params.DocumentContent), params.LineIndex,
	)
	bindings, err := p.adminIndexer.ResolveTwigVueBindingsForComponent(
		params.Root, params.DocumentContent, offset,
		templatePath, liveComponent,
	)
	if err != nil {
		return nil
	}
	return bindings
}

func adminTemplatePath(uri string) string {
	path, err := uriutil.Path(uri)
	if err != nil {
		return ""
	}
	return path
}

func adminTwigVueExpressionAtCompletion(params *lsp.CompletionRequest) bool {
	if params == nil || params.Node == nil || params.LineIndex == nil ||
		params.CompletionParams == nil {
		return false
	}
	offset := params.LineIndex.OffsetUTF16(
		uint32(params.Position.Line), uint32(params.Position.Character),
	)
	return admin.IsTwigVueExpressionAt(params.Node, offset)
}

func (p *AdminCompletionProvider) scopedSlotsAtCompletion(
	params *lsp.CompletionRequest,
) ([]admin.ResolvedTwigScopedSlot, uint32) {
	if p.adminIndexer == nil || params == nil || params.Root == nil ||
		params.LineIndex == nil || params.CompletionParams == nil {
		return nil, 0
	}
	offset := params.LineIndex.OffsetUTF16(
		uint32(params.Position.Line), uint32(params.Position.Character),
	)
	templatePath := adminTemplatePath(params.TextDocument.URI)
	liveOwner, _ := p.adminIndexer.GetComponentForDocument(
		templatePath, params.Root, string(params.DocumentContent), params.LineIndex,
	)
	resolved, err := p.adminIndexer.ResolveTwigScopedSlotsForOwner(
		params.Root, offset, templatePath, liveOwner,
	)
	if err != nil {
		return nil, offset
	}
	return resolved, offset
}

func scopedSlotContractCompletions(
	resolved admin.ResolvedTwigScopedSlot,
) []protocol.CompletionItem {
	items := make([]protocol.CompletionItem, 0, len(resolved.Slot.Members))
	for _, member := range resolved.Slot.Members {
		detail := "slot prop · " + resolved.QualifiedName()
		if member.Type != "" {
			detail += " · " + member.Type
		}
		item := protocol.CompletionItem{
			Label: member.Name, Kind: int(protocol.PropertyCompletion),
			Detail: detail,
		}
		item.Documentation.Kind = string(protocol.Markdown)
		item.Documentation.Value = "Exposed by scoped slot `" +
			resolved.QualifiedName() + "`."
		if resolved.Slot.IsDynamicName() {
			item.Documentation.Value += "\n\nProvided by dynamic family `" +
				resolved.Slot.DisplayName() + "`."
		}
		items = append(items, item)
	}
	sortCompletionItems(items)
	return items
}

func scopedSlotLocalCompletions(
	resolved admin.ResolvedTwigScopedSlot,
) []protocol.CompletionItem {
	items := make([]protocol.CompletionItem, 0, len(resolved.Scope.Bindings))
	seen := make(map[string]bool, len(resolved.Scope.Bindings))
	for _, binding := range resolved.Scope.Bindings {
		if binding.LocalName == "" || seen[binding.LocalName] {
			continue
		}
		seen[binding.LocalName] = true
		detail := "slot local · " + resolved.QualifiedName()
		member, found := resolved.Slot.Member(binding.MemberName)
		memberType := ""
		if binding.WholeObject {
			memberType = resolved.Slot.PayloadType
		} else if found {
			memberType = member.Type
		}
		if memberType != "" {
			detail += " · " + memberType
		}
		item := protocol.CompletionItem{
			Label: binding.LocalName, Kind: int(protocol.VariableCompletion),
			Detail: detail,
		}
		item.Documentation.Kind = string(protocol.Markdown)
		item.Documentation.Value = "Lexically bound by scoped slot `" +
			resolved.QualifiedName() + "`."
		if resolved.Slot.IsDynamicName() {
			item.Documentation.Value += "\n\nProvided by dynamic family `" +
				resolved.Slot.DisplayName() + "`."
		}
		items = append(items, item)
	}
	sortCompletionItems(items)
	return items
}

func twigVueBindingCompletions(
	bindings []admin.TwigVueBinding,
) []protocol.CompletionItem {
	items := make([]protocol.CompletionItem, 0, len(bindings))
	for _, binding := range bindings {
		if binding.Name == "" {
			continue
		}
		detail := "v-for local"
		documentation := "Introduced by the enclosing `v-for` expression."
		if binding.Kind == admin.TwigVueBindingEvent {
			detail = "event payload"
			if binding.ComponentName != "" && binding.EventName != "" {
				detail += " · " + binding.ComponentName + "." + binding.EventName
			}
			documentation = "Implicit payload of the current Vue event handler."
		} else if binding.Iterable != "" {
			detail += " · " + binding.Iterable
		}
		if binding.Type != "" {
			detail += " · " + binding.Type
		}
		item := protocol.CompletionItem{
			Label: binding.Name, Kind: int(protocol.VariableCompletion),
			Detail: detail,
		}
		item.Documentation.Kind = string(protocol.Markdown)
		item.Documentation.Value = documentation
		items = append(items, item)
	}
	sortCompletionItems(items)
	return items
}

type adminLexicalCompletionLayer struct {
	scope uint32
	items []protocol.CompletionItem
}

// mergeAdminLexicalCompletions applies Vue's normal lexical shadowing: outer
// component members first, then increasingly smaller v-for/event/v-slot
// scopes. The innermost binding therefore owns a duplicate completion label.
func mergeAdminLexicalCompletions(
	base []protocol.CompletionItem,
	slots []admin.ResolvedTwigScopedSlot,
	bindings []admin.TwigVueBinding,
) []protocol.CompletionItem {
	var layers []adminLexicalCompletionLayer
	for slotIndex := range slots {
		slot := slots[slotIndex]
		layers = append(layers, adminLexicalCompletionLayer{
			scope: slot.Scope.TemplateRange.Len(),
			items: scopedSlotLocalCompletions(slot),
		})
	}
	byScope := make(map[admin.TwigVueBindingKind]map[cst.TextRange][]admin.TwigVueBinding)
	for _, binding := range bindings {
		if byScope[binding.Kind] == nil {
			byScope[binding.Kind] = make(map[cst.TextRange][]admin.TwigVueBinding)
		}
		byScope[binding.Kind][binding.ScopeRange] = append(
			byScope[binding.Kind][binding.ScopeRange], binding,
		)
	}
	for _, scopes := range byScope {
		for scope, values := range scopes {
			layers = append(layers, adminLexicalCompletionLayer{
				scope: scope.Len(), items: twigVueBindingCompletions(values),
			})
		}
	}
	sort.SliceStable(layers, func(left, right int) bool {
		return layers[left].scope > layers[right].scope
	})
	result := base
	for _, layer := range layers {
		result = mergeAdminCompletions(result, layer.items)
	}
	return result
}

// mergeAdminCompletions overlays right by label, giving lexical bindings the
// expected precedence over component members with the same name.
func mergeAdminCompletions(
	left, right []protocol.CompletionItem,
) []protocol.CompletionItem {
	itemsByLabel := make(map[string]protocol.CompletionItem, len(left)+len(right))
	for _, item := range left {
		itemsByLabel[item.Label] = item
	}
	for _, item := range right {
		itemsByLabel[item.Label] = item
	}
	items := make([]protocol.CompletionItem, 0, len(itemsByLabel))
	for _, item := range itemsByLabel {
		items = append(items, item)
	}
	sortCompletionItems(items)
	return items
}

func adminTwigRegistryReferenceAtCompletion(
	params *lsp.CompletionRequest,
) (admin.AdminTwigRegistryReference, bool) {
	if params == nil || params.Root == nil || params.LineIndex == nil ||
		params.CompletionParams == nil {
		return admin.AdminTwigRegistryReference{}, false
	}
	offset := params.LineIndex.OffsetUTF16(
		uint32(params.Position.Line),
		uint32(params.Position.Character),
	)
	return admin.TwigRegistryReferenceAtOffset(params.Root, offset)
}

func (p *AdminCompletionProvider) getTemplateMemberCompletions(
	uri string,
) []protocol.CompletionItem {
	return p.getTemplateMemberCompletionsAt(uri, nil)
}

func (p *AdminCompletionProvider) getTemplateMemberCompletionsAt(
	uri string,
	node *twigsyntax.Node,
	documents ...*lsp.TextDocument,
) []protocol.CompletionItem {
	if p.adminIndexer == nil {
		return nil
	}
	path, err := uriutil.Path(uri)
	if err != nil {
		return nil
	}
	var component *admin.VueComponent
	if len(documents) > 0 && documents[0] != nil &&
		documents[0].SyntaxTree != nil {
		document := documents[0]
		component, err = p.adminIndexer.GetComponentForDocument(
			path, document.SyntaxTree.Root, document.Source, document.LineIndex,
		)
	} else {
		component, err = p.adminIndexer.GetComponentByTemplatePath(path)
	}
	if err != nil || component == nil {
		return nil
	}
	itemsByName := make(map[string]protocol.CompletionItem)
	for _, member := range component.TemplateMembers() {
		item := protocol.CompletionItem{
			Label:  member.Name,
			Kind:   adminMemberCompletionKind(member.Kind),
			Detail: adminMemberDetail(member),
		}
		if member.Kind == admin.ComponentMemberMethod {
			item.InsertText = member.Name + "($0)"
			item.InsertTextFormat = int(protocol.SnippetTextFormat)
		}
		item.Documentation.Kind = string(protocol.Markdown)
		item.Documentation.Value = "Provided by Administration component `" +
			component.Name + "`."
		markAdminCompletionDeprecated(&item, member.Deprecated)
		if member.Kind == admin.ComponentMemberProp && !item.Deprecated {
			if prop, found := component.ComponentProp(member.Name); found {
				markAdminCompletionDeprecated(&item, prop.Deprecated)
			}
		}
		itemsByName[member.Name] = item
	}
	addRuntime := func(member admin.VueComponentMember, detail, documentation string) {
		if member.Name == "" {
			return
		}
		if _, exists := itemsByName[member.Name]; exists {
			return
		}
		item := protocol.CompletionItem{
			Label: member.Name, Kind: adminMemberCompletionKind(member.Kind),
			Detail: detail,
		}
		if member.Kind == admin.ComponentMemberMethod {
			item.InsertText = member.Name + "($0)"
			item.InsertTextFormat = int(protocol.SnippetTextFormat)
		}
		item.Documentation.Kind = string(protocol.Markdown)
		item.Documentation.Value = documentation
		if member.Type != "" {
			item.Documentation.Value += "\n\nType: `" + member.Type + "`."
		}
		itemsByName[member.Name] = item
	}
	for _, member := range admin.VueBuiltinMembers() {
		addRuntime(
			member,
			"Vue template instance member",
			"Available on the Administration Vue component instance.",
		)
	}
	for _, global := range admin.VueTemplateGlobals() {
		addRuntime(
			global,
			"JavaScript template global",
			"Available in Administration Vue template expressions.",
		)
	}
	if node != nil {
		if blockName := twigquery.BlockName(twigquery.BlockAt(node)); blockName != "" {
			if block, found := component.ComponentBlock(blockName); found {
				for _, member := range block.ScopeMembers {
					if member.Name == "" {
						continue
					}
					item := protocol.CompletionItem{
						Label:  member.Name,
						Kind:   int(protocol.VariableCompletion),
						Detail: "Twig block scope • " + block.Name,
					}
					item.Documentation.Kind = string(protocol.Markdown)
					item.Documentation.Value = "Provided by Administration Twig block `" +
						block.Name + "`."
					if member.Type != "" {
						item.Documentation.Value += "\n\nType: `" + member.Type + "`."
					}
					// Lexical block inputs shadow equally named component members.
					itemsByName[member.Name] = item
				}
			}
		}
	}
	items := make([]protocol.CompletionItem, 0, len(itemsByName))
	for _, item := range itemsByName {
		items = append(items, item)
	}
	sortCompletionItems(items)
	return items
}

func adminMemberCompletionKind(kind admin.VueComponentMemberKind) int {
	switch kind {
	case admin.ComponentMemberMethod:
		return int(protocol.MethodCompletion)
	case admin.ComponentMemberComputed:
		return int(protocol.PropertyCompletion)
	case admin.ComponentMemberInject:
		return int(protocol.ReferenceCompletion)
	default:
		return int(protocol.VariableCompletion)
	}
}

func adminMemberDetail(member admin.VueComponentMember) string {
	detail := string(member.Kind)
	if member.Type != "" {
		detail += " • " + member.Type
	}
	return detail
}

// isInHTMLTagName checks if the cursor is in an HTML tag name position
