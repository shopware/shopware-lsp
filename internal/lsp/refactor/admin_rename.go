package refactor

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/admin"
	"github.com/shopware/shopware-lsp/internal/language"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	twigast "github.com/shopware/shopware-lsp/internal/parser/twig/ast"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

var (
	adminComponentNamePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]*$`)
	adminIdentifierPattern    = regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$]*$`)
	adminPropNamePattern      = regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$]*$`)
	adminEventNamePattern     = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.:-]*$`)
	adminSlotNamePattern      = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.-]*$`)
	adminDirectiveNamePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]*$`)
)

// AdminRenameProvider renames registry identities whose declarations and all
// supported usages are represented by one source token. Derived identities
// such as privilege roles and module route names intentionally remain
// references-only.
type AdminRenameProvider struct {
	index *admin.AdminComponentIndexer
}

func NewAdminRenameProvider(
	index *admin.AdminComponentIndexer,
) *AdminRenameProvider {
	return &AdminRenameProvider{index: index}
}

func (p *AdminRenameProvider) Rename(
	_ context.Context,
	request *lsp.RenameRequest,
) (*protocol.WorkspaceEdit, error) {
	if p == nil || p.index == nil || request == nil || request.Root == nil {
		return nil, nil
	}
	if edit, handled, err := p.renameAdminLocalComponentDeclaration(
		request,
	); handled || err != nil {
		return edit, err
	}
	if edit, handled, err := p.renameAdminLocalComponentTag(
		request,
	); handled || err != nil {
		return edit, err
	}
	if edit, handled, err := p.renameAdminScopedSlotLocal(
		request,
	); handled || err != nil {
		return edit, err
	}
	if edit, handled, err := renameAdminVueLocal(request); handled || err != nil {
		return edit, err
	}
	target, dynamicSlotRanges, dynamicSlotHandled, err :=
		p.dynamicTwigSlotRenameTarget(request)
	if err != nil {
		return nil, err
	}
	found := dynamicSlotHandled && target.Name != ""
	if !dynamicSlotHandled {
		target, found, err = p.targetAt(request)
	}
	if err != nil || !found {
		return nil, err
	}
	if target.Kind == admin.AdminSymbolComponentModel {
		return nil, fmt.Errorf(
			"cannot safely rename compound v-model contracts; rename the prop and update event declarations together",
		)
	}
	switch target.Kind {
	case admin.AdminSymbolComponent, admin.AdminSymbolService,
		admin.AdminSymbolStore, admin.AdminSymbolMixin, admin.AdminSymbolDirective,
		admin.AdminSymbolFilter,
		admin.AdminSymbolCMSElement, admin.AdminSymbolCMSBlock,
		admin.AdminSymbolEventBusEvent,
		admin.AdminSymbolComponentProp,
		admin.AdminSymbolComponentEvent, admin.AdminSymbolComponentSlot,
		admin.AdminSymbolComponentMember:
	default:
		return nil, nil
	}
	newName := strings.TrimSpace(request.NewName)
	if target.Kind == admin.AdminSymbolComponentProp {
		newName = admin.NormalizePropName(newName)
	}
	if target.Kind == admin.AdminSymbolComponentEvent {
		newName = admin.CanonicalEventName(newName)
	}
	if newName == target.Name {
		return &protocol.WorkspaceEdit{}, nil
	}
	if err := p.validateRename(target, newName); err != nil {
		return nil, err
	}
	if err := p.rejectConflict(target, newName); err != nil {
		return nil, err
	}
	sets, err := p.index.GetSymbolUsages(target)
	if err != nil {
		return nil, err
	}
	var eventBusDeclaration admin.TwigVueMember
	if target.Kind == admin.AdminSymbolEventBusEvent {
		var found bool
		eventBusDeclaration, found, err = p.index.ResolveShopwareEventBusEvent(
			target.Name, "",
		)
		if err != nil {
			return nil, err
		}
		if !found || eventBusDeclaration.DefinitionPath == "" ||
			(!eventBusDeclaration.DefinitionRange.Declaration &&
				!eventBusDeclaration.DefinitionRange.Identifier) {
			return nil, fmt.Errorf(
				"cannot safely rename Shopware EventBus event %q because its typed declaration is not indexed",
				target.Name,
			)
		}
	}
	if target.Kind == admin.AdminSymbolComponentProp ||
		target.Kind == admin.AdminSymbolComponentEvent {
		for _, set := range sets {
			if set.Kind == admin.AdminSymbolComponentModel {
				return nil, fmt.Errorf(
					"cannot safely rename Administration %s %q because it participates in a compound v-model contract",
					target.Kind,
					target.Name,
				)
			}
		}
	}
	if target.Kind == admin.AdminSymbolComponentProp ||
		target.Kind == admin.AdminSymbolComponentEvent ||
		target.Kind == admin.AdminSymbolComponentSlot {
		for _, set := range sets {
			for _, occurrence := range set.Occurrences {
				safe, safeErr := p.index.DynamicComponentUsageRenameSafe(
					set, occurrence, target,
				)
				if safeErr != nil {
					return nil, safeErr
				}
				if !safe {
					return nil, fmt.Errorf(
						"cannot safely rename Administration %s %q because a dynamic component usage resolves to distinct declarations",
						target.Kind,
						target.Name,
					)
				}
			}
		}
	}
	if len(sets) == 0 && eventBusDeclaration.DefinitionPath == "" {
		return nil, nil
	}
	if (target.Kind == admin.AdminSymbolComponentProp ||
		target.Kind == admin.AdminSymbolComponentEvent ||
		target.Kind == admin.AdminSymbolComponentSlot) &&
		!hasAdminOwnedSourceOccurrence(sets, target.Owner) {
		return nil, fmt.Errorf(
			"cannot safely rename Administration %s %q because its declaration source is not indexed",
			target.Kind,
			target.Name,
		)
	}
	if target.Kind == admin.AdminSymbolComponentMember &&
		!hasAdminDeclarationOccurrence(sets) {
		return nil, fmt.Errorf(
			"cannot safely rename Administration component member %q because its declaration source is not indexed",
			target.Name,
		)
	}
	if target.Kind == admin.AdminSymbolDirective && target.Owner != "" &&
		!hasAdminDeclarationOccurrence(sets) {
		return nil, fmt.Errorf(
			"cannot safely rename component-local Administration directive %q because its declaration source is not indexed",
			target.Name,
		)
	}
	if (target.Kind == admin.AdminSymbolCMSElement ||
		target.Kind == admin.AdminSymbolCMSBlock) &&
		!hasAdminDeclarationOccurrence(sets) {
		return nil, fmt.Errorf(
			"cannot safely rename Shopware CMS registry %q because its declaration source is not indexed",
			target.Name,
		)
	}
	if target.Kind == admin.AdminSymbolService && !adminIdentifierPattern.MatchString(newName) {
		for _, set := range sets {
			for _, occurrence := range set.Occurrences {
				if occurrence.Identifier {
					return nil, fmt.Errorf(
						"%q is not a valid JavaScript identifier for injected service %s",
						newName, target.Name,
					)
				}
			}
		}
	}
	changes := make(map[string][]protocol.TextEdit)
	if eventBusDeclaration.DefinitionPath != "" {
		changes[uriutil.FileURI(eventBusDeclaration.DefinitionPath)] = append(
			changes[uriutil.FileURI(eventBusDeclaration.DefinitionPath)],
			protocol.TextEdit{
				Range: protocol.Range{
					Start: protocol.Position{
						Line:      eventBusDeclaration.DefinitionRange.StartLine,
						Character: eventBusDeclaration.DefinitionRange.StartCharacter,
					},
					End: protocol.Position{
						Line:      eventBusDeclaration.DefinitionRange.EndLine,
						Character: eventBusDeclaration.DefinitionRange.EndCharacter,
					},
				},
				NewText: newName,
			},
		)
	}
	for _, rangeValue := range dynamicSlotRanges {
		changes[request.TextDocument.URI] = append(
			changes[request.TextDocument.URI],
			protocol.TextEdit{
				Range:   adminRenameProtocolRange(request.LineIndex, rangeValue),
				NewText: newName,
			},
		)
	}
	liveTemplatePath := ""
	if target.Kind == admin.AdminSymbolComponentMember &&
		adminRenameTemplate(request) {
		liveTemplatePath, err = uriutil.Path(request.TextDocument.URI)
		if err != nil {
			return nil, err
		}
		for _, identifier := range admin.TwigVueExpressionRootIdentifiers(
			request.Root, request.DocumentContent,
		) {
			candidate, _, candidateFound, resolveErr := p.index.TwigComponentMemberAt(
				liveTemplatePath,
				request.Root,
				request.DocumentContent,
				identifier.Range.Start,
			)
			if resolveErr != nil {
				return nil, resolveErr
			}
			if !candidateFound || candidate != target {
				continue
			}
			changes[request.TextDocument.URI] = append(
				changes[request.TextDocument.URI],
				protocol.TextEdit{
					Range: adminRenameProtocolRange(
						request.LineIndex, identifier.Range,
					),
					NewText: newName,
				},
			)
		}
	}
	for _, set := range sets {
		if liveTemplatePath != "" &&
			filepath.Clean(set.FilePath) == filepath.Clean(liveTemplatePath) {
			continue
		}
		uri := uriutil.FileURI(set.FilePath)
		for _, occurrence := range set.Occurrences {
			replacement := adminRenameReplacement(target, newName, occurrence)
			changes[uri] = append(changes[uri], protocol.TextEdit{
				Range: protocol.Range{
					Start: protocol.Position{
						Line:      occurrence.StartLine,
						Character: occurrence.StartCharacter,
					},
					End: protocol.Position{
						Line:      occurrence.EndLine,
						Character: occurrence.EndCharacter,
					},
				},
				NewText: replacement,
			})
		}
	}
	for uri := range changes {
		seenEdits := make(map[string]bool, len(changes[uri]))
		uniqueEdits := changes[uri][:0]
		for _, edit := range changes[uri] {
			key := fmt.Sprintf(
				"%d:%d:%d:%d:%s",
				edit.Range.Start.Line, edit.Range.Start.Character,
				edit.Range.End.Line, edit.Range.End.Character, edit.NewText,
			)
			if seenEdits[key] {
				continue
			}
			seenEdits[key] = true
			uniqueEdits = append(uniqueEdits, edit)
		}
		changes[uri] = uniqueEdits
		sort.SliceStable(changes[uri], func(left, right int) bool {
			leftStart := changes[uri][left].Range.Start
			rightStart := changes[uri][right].Range.Start
			if leftStart.Line != rightStart.Line {
				return leftStart.Line > rightStart.Line
			}
			return leftStart.Character > rightStart.Character
		})
	}
	return &protocol.WorkspaceEdit{Changes: changes}, nil
}

func (p *AdminRenameProvider) renameAdminScopedSlotLocal(
	request *lsp.RenameRequest,
) (*protocol.WorkspaceEdit, bool, error) {
	if p == nil || p.index == nil || request == nil || request.Root == nil ||
		request.LineIndex == nil || request.RenameParams == nil ||
		!adminRenameTemplate(request) {
		return nil, false, nil
	}
	templatePath, err := uriutil.Path(request.TextDocument.URI)
	if err != nil {
		return nil, false, err
	}
	offset := request.LineIndex.OffsetUTF16(
		uint32(request.Position.Line), uint32(request.Position.Character),
	)
	liveOwner, err := p.index.GetComponentForDocument(
		templatePath, request.Root, string(request.DocumentContent), request.LineIndex,
	)
	if err != nil {
		return nil, false, err
	}
	if member, resolveErr := p.index.ResolveTwigScopedSlotMemberForOwner(
		request.Root, request.Node, request.DocumentContent, offset,
		templatePath, liveOwner,
	); resolveErr != nil {
		return nil, true, resolveErr
	} else if member != nil {
		return nil, true, fmt.Errorf(
			"cannot rename scoped-slot contract member %q from a consumer template",
			member.Access.Member,
		)
	}
	resolved, err := p.index.ResolveTwigScopedSlotBindingForOwner(
		request.Root, request.Node, request.DocumentContent, offset,
		templatePath, liveOwner,
	)
	if err != nil || resolved == nil {
		return nil, resolved != nil, err
	}
	if resolved.Scope.IsBindingOffset(offset) &&
		resolved.Identifier == resolved.Binding.MemberName &&
		resolved.Binding.MemberName != resolved.Binding.LocalName {
		return nil, true, fmt.Errorf(
			"cannot rename scoped-slot contract member %q from a consumer template",
			resolved.Binding.MemberName,
		)
	}
	newName := strings.TrimSpace(request.NewName)
	if newName == resolved.Binding.LocalName {
		return &protocol.WorkspaceEdit{}, true, nil
	}
	if !adminIdentifierPattern.MatchString(newName) {
		return nil, true, fmt.Errorf(
			"%q is not a valid scoped-slot local identifier", newName,
		)
	}
	for _, binding := range resolved.Scope.Bindings {
		if binding.LocalName == newName &&
			binding.LocalRange != resolved.Binding.LocalRange {
			return nil, true, fmt.Errorf(
				"scoped-slot local %q already exists in this slot scope", newName,
			)
		}
	}
	ranges := admin.TwigScopedSlotBindingReferences(
		request.Root, request.DocumentContent, *resolved,
	)
	if len(ranges) == 0 {
		return nil, true, nil
	}
	edits := make([]protocol.TextEdit, 0, len(ranges))
	for _, rangeValue := range ranges {
		replacement := newName
		if rangeValue == resolved.Binding.LocalRange &&
			!resolved.Binding.WholeObject &&
			resolved.Binding.MemberName == resolved.Binding.LocalName {
			replacement = resolved.Binding.MemberName + ": " + newName
		}
		edits = append(edits, protocol.TextEdit{
			Range:   adminRenameProtocolRange(request.LineIndex, rangeValue),
			NewText: replacement,
		})
	}
	changes := map[string][]protocol.TextEdit{
		request.TextDocument.URI: edits,
	}
	sortAdminTextEdits(changes)
	return &protocol.WorkspaceEdit{Changes: changes}, true, nil
}

func (p *AdminRenameProvider) dynamicTwigSlotRenameTarget(
	request *lsp.RenameRequest,
) (admin.AdminSymbolTarget, []cst.TextRange, bool, error) {
	if p == nil || p.index == nil || request == nil || request.Root == nil ||
		request.LineIndex == nil ||
		!adminRenameTemplate(request) {
		return admin.AdminSymbolTarget{}, nil, false, nil
	}
	offset := request.LineIndex.OffsetUTF16(
		uint32(request.Position.Line), uint32(request.Position.Character),
	)
	attribute := twigquery.HTMLAttributeAt(request.Root.NodeAtOffset(offset))
	if attribute == nil {
		return admin.AdminSymbolTarget{}, nil, false, nil
	}
	attributeName := twigquery.HTMLAttributeName(attribute)
	if admin.NormalizeSlotName(attributeName) == "" {
		return admin.AdminSymbolTarget{}, nil, false, nil
	}
	startTag := twigquery.StartingHTMLTagAt(attribute)
	owner := admin.TwigSlotOwnerStartingTag(startTag)
	if _, dynamic := admin.TwigDynamicComponentSelector(owner); !dynamic {
		return admin.AdminSymbolTarget{}, nil, false, nil
	}
	attributeNode, ok := twigast.CastHtmlAttribute(attribute)
	if !ok || attributeNode.Name() == nil {
		return admin.AdminSymbolTarget{}, nil, true, nil
	}
	reference, found := admin.VueSlotReferenceForAttribute(
		attributeName, attributeNode.Name().Range(),
	)
	if !found || offset < reference.Range.Start || offset > reference.Range.End {
		return admin.AdminSymbolTarget{}, nil, true, nil
	}
	templatePath, err := uriutil.Path(request.TextDocument.URI)
	if err != nil {
		return admin.AdminSymbolTarget{}, nil, true, err
	}
	liveOwner, err := p.index.GetComponentForDocument(
		templatePath, request.Root, string(request.DocumentContent), request.LineIndex,
	)
	if err != nil {
		return admin.AdminSymbolTarget{}, nil, true, err
	}
	components, complete, err :=
		p.index.ResolveTwigSlotConsumerComponents(
			templatePath, startTag, liveOwner,
		)
	if err != nil {
		return admin.AdminSymbolTarget{}, nil, true, err
	}
	if !complete {
		return admin.AdminSymbolTarget{}, nil, true, fmt.Errorf(
			"cannot safely rename an Administration slot on a runtime-dynamic component",
		)
	}
	targets := adminSlotRenameTargets(components, reference.Name)
	if len(targets) == 0 {
		return admin.AdminSymbolTarget{}, nil, true, nil
	}
	if len(targets) > 1 {
		return admin.AdminSymbolTarget{}, nil, true, fmt.Errorf(
			"cannot safely rename Administration slot %q because the dynamic component candidates expose distinct declarations",
			reference.Name,
		)
	}
	ranges, err := p.dynamicTwigSlotRenameRanges(
		request.Root, templatePath, liveOwner, targets[0],
	)
	return targets[0], ranges, true, err
}

func adminSlotRenameTargets(
	components []admin.VueComponent,
	slotName string,
) []admin.AdminSymbolTarget {
	var result []admin.AdminSymbolTarget
	seen := make(map[admin.AdminSymbolTarget]bool)
	for _, component := range components {
		slot, found := component.ComponentSlot(slotName)
		if !found {
			continue
		}
		owner := slot.FilePath
		if owner == "" {
			owner = component.TemplatePath
		}
		target := admin.AdminSymbolTarget{
			Kind: admin.AdminSymbolComponentSlot, Owner: owner, Name: slotName,
		}
		if target.Owner == "" || seen[target] {
			continue
		}
		seen[target] = true
		result = append(result, target)
	}
	return result
}

func (p *AdminRenameProvider) dynamicTwigSlotRenameRanges(
	root *twigsyntax.Node,
	templatePath string,
	liveOwner *admin.VueComponent,
	target admin.AdminSymbolTarget,
) ([]cst.TextRange, error) {
	var result []cst.TextRange
	for _, startTag := range twigquery.Nodes(
		root, twigsyntax.HtmlStartingTag,
	) {
		owner := admin.TwigSlotOwnerStartingTag(startTag)
		if _, dynamic := admin.TwigDynamicComponentSelector(owner); !dynamic {
			continue
		}
		components, complete, err :=
			p.index.ResolveTwigSlotConsumerComponents(
				templatePath, startTag, liveOwner,
			)
		if err != nil {
			return nil, err
		}
		if !complete {
			continue
		}
		tag, ok := twigast.CastHtmlStartingTag(startTag)
		if !ok {
			continue
		}
		for _, attribute := range tag.Attributes() {
			nameToken := attribute.Name()
			if nameToken == nil {
				continue
			}
			name := twigquery.HTMLAttributeName(attribute.Syntax())
			reference, found := admin.VueSlotReferenceForAttribute(
				name, nameToken.Range(),
			)
			if !found {
				continue
			}
			for _, candidate := range adminSlotRenameTargets(
				components, reference.Name,
			) {
				if candidate == target {
					result = append(result, reference.Range)
					break
				}
			}
		}
	}
	return result, nil
}

func (p *AdminRenameProvider) renameAdminLocalComponentDeclaration(
	request *lsp.RenameRequest,
) (*protocol.WorkspaceEdit, bool, error) {
	if p == nil || p.index == nil || request == nil || request.Root == nil ||
		request.LineIndex == nil || request.RenameParams == nil {
		return nil, false, nil
	}
	ext := strings.ToLower(filepath.Ext(request.TextDocument.URI))
	if ext != ".js" && ext != ".ts" &&
		(ext != ".vue" || !adminRenameScript(request)) {
		return nil, false, nil
	}
	definitionPath, err := uriutil.Path(request.TextDocument.URI)
	if err != nil {
		return nil, false, err
	}
	owner, local, found, err := p.index.GetLocalComponentAtDefinitionPosition(
		definitionPath,
		request.Position.Line,
		request.Position.Character,
	)
	if err != nil || !found || owner == nil {
		return nil, false, err
	}
	newName := strings.TrimSpace(request.NewName)
	if newName == local.Name {
		return &protocol.WorkspaceEdit{}, true, nil
	}
	if !adminComponentNamePattern.MatchString(newName) {
		return nil, true, fmt.Errorf(
			"%q is not a valid Administration component name", newName,
		)
	}
	if conflict, exists := owner.LocalComponent(newName); exists &&
		conflict.Name != local.Name {
		return nil, true, fmt.Errorf(
			"local Administration component %q already exists", newName,
		)
	}
	if !adminLocalComponentHasDeclarationRange(local) {
		return nil, true, fmt.Errorf(
			"cannot safely rename local Administration component %q because its declaration range is not indexed",
			local.Name,
		)
	}
	changes := make(map[string][]protocol.TextEdit)
	sets, err := p.index.GetUsages(
		admin.AdminSymbolComponent, "", local.Name,
	)
	if err != nil {
		return nil, true, err
	}
	for _, set := range sets {
		if filepath.Clean(set.FilePath) != filepath.Clean(owner.TemplatePath) {
			continue
		}
		uri := uriutil.FileURI(set.FilePath)
		for _, occurrence := range set.Occurrences {
			changes[uri] = append(changes[uri], protocol.TextEdit{
				Range: protocol.Range{
					Start: protocol.Position{
						Line:      occurrence.StartLine,
						Character: occurrence.StartCharacter,
					},
					End: protocol.Position{
						Line:      occurrence.EndLine,
						Character: occurrence.EndCharacter,
					},
				},
				NewText: newName,
			})
		}
	}
	definitionURI := uriutil.FileURI(local.FilePath)
	changes[definitionURI] = append(changes[definitionURI], protocol.TextEdit{
		Range:   adminLocalComponentDeclarationRange(local),
		NewText: adminLocalComponentDeclarationReplacement(local, newName),
	})
	sortAdminTextEdits(changes)
	return &protocol.WorkspaceEdit{Changes: changes}, true, nil
}

func (p *AdminRenameProvider) renameAdminLocalComponentTag(
	request *lsp.RenameRequest,
) (*protocol.WorkspaceEdit, bool, error) {
	if p == nil || p.index == nil || request == nil || request.Root == nil ||
		request.LineIndex == nil || request.RenameParams == nil ||
		!adminRenameTemplate(request) {
		return nil, false, nil
	}
	templatePath, err := uriutil.Path(request.TextDocument.URI)
	if err != nil {
		return nil, false, err
	}
	offset := request.LineIndex.OffsetUTF16(
		uint32(request.Position.Line), uint32(request.Position.Character),
	)
	target, found := admin.TwigSymbolAtOffset(request.Root, offset)
	if !found || target.Kind != admin.AdminSymbolComponent {
		return nil, false, nil
	}
	owner, err := p.index.GetComponentByTemplatePath(templatePath)
	if err != nil || owner == nil {
		return nil, false, err
	}
	local, found := owner.LocalComponent(target.Name)
	if !found {
		return nil, false, nil
	}
	newName := strings.TrimSpace(request.NewName)
	if newName == local.Name {
		return &protocol.WorkspaceEdit{}, true, nil
	}
	if !adminComponentNamePattern.MatchString(newName) {
		return nil, true, fmt.Errorf(
			"%q is not a valid Administration component name", newName,
		)
	}
	if conflict, exists := owner.LocalComponent(newName); exists &&
		conflict.Name != local.Name {
		return nil, true, fmt.Errorf(
			"local Administration component %q already exists", newName,
		)
	}
	if !adminLocalComponentHasDeclarationRange(local) {
		return nil, true, fmt.Errorf(
			"cannot safely rename local Administration component %q because its declaration range is not indexed",
			local.Name,
		)
	}

	changes := map[string][]protocol.TextEdit{}
	for _, node := range twigquery.Nodes(
		request.Root,
		twigsyntax.HtmlStartingTag,
		twigsyntax.HtmlEndingTag,
	) {
		var nameRange *protocol.Range
		switch node.Kind() {
		case twigsyntax.HtmlStartingTag:
			tag, ok := twigast.CastHtmlStartingTag(node)
			if ok && tag.Name() != nil && tag.Name().Text() == local.Name {
				value := adminRenameProtocolRange(
					request.LineIndex, tag.Name().Range(),
				)
				nameRange = &value
			}
			if selector, dynamic := admin.TwigDynamicComponentSelector(node); dynamic {
				for _, candidate := range selector.Candidates {
					if candidate.Name != local.Name {
						continue
					}
					changes[request.TextDocument.URI] = append(
						changes[request.TextDocument.URI],
						protocol.TextEdit{
							Range: adminRenameProtocolRange(
								request.LineIndex, candidate.Range,
							),
							NewText: newName,
						},
					)
				}
			}
		case twigsyntax.HtmlEndingTag:
			tag, ok := twigast.CastHtmlEndingTag(node)
			if ok && tag.Name() != nil && tag.Name().Text() == local.Name {
				value := adminRenameProtocolRange(
					request.LineIndex, tag.Name().Range(),
				)
				nameRange = &value
			}
		}
		if nameRange != nil {
			changes[request.TextDocument.URI] = append(
				changes[request.TextDocument.URI],
				protocol.TextEdit{Range: *nameRange, NewText: newName},
			)
		}
	}
	definitionURI := uriutil.FileURI(local.FilePath)
	changes[definitionURI] = append(changes[definitionURI], protocol.TextEdit{
		Range:   adminLocalComponentDeclarationRange(local),
		NewText: adminLocalComponentDeclarationReplacement(local, newName),
	})
	sortAdminTextEdits(changes)
	return &protocol.WorkspaceEdit{Changes: changes}, true, nil
}

func adminLocalComponentHasDeclarationRange(
	local admin.VueLocalComponent,
) bool {
	return local.FilePath != "" &&
		(local.NameRange.EndLine != 0 ||
			local.NameRange.EndCharacter != 0 ||
			local.NameRange.StartLine != 0 ||
			local.NameRange.StartCharacter != 0)
}

func adminLocalComponentDeclarationRange(
	local admin.VueLocalComponent,
) protocol.Range {
	return protocol.Range{
		Start: protocol.Position{
			Line:      local.NameRange.StartLine,
			Character: local.NameRange.StartCharacter,
		},
		End: protocol.Position{
			Line:      local.NameRange.EndLine,
			Character: local.NameRange.EndCharacter,
		},
	}
}

func adminLocalComponentDeclarationReplacement(
	local admin.VueLocalComponent,
	newName string,
) string {
	if local.Shorthand {
		return "'" + newName + "': " + local.Symbol
	}
	if !local.Quoted {
		return "'" + newName + "'"
	}
	return newName
}

func sortAdminTextEdits(changes map[string][]protocol.TextEdit) {
	for uri := range changes {
		sort.SliceStable(changes[uri], func(left, right int) bool {
			leftStart := changes[uri][left].Range.Start
			rightStart := changes[uri][right].Range.Start
			if leftStart.Line != rightStart.Line {
				return leftStart.Line > rightStart.Line
			}
			return leftStart.Character > rightStart.Character
		})
	}
}

func adminRenameProtocolRange(
	lineIndex *cst.LineIndex,
	rangeValue cst.TextRange,
) protocol.Range {
	startLine, startCharacter := lineIndex.PositionUTF16(rangeValue.Start)
	endLine, endCharacter := lineIndex.PositionUTF16(rangeValue.End)
	return protocol.Range{
		Start: protocol.Position{
			Line: int(startLine), Character: int(startCharacter),
		},
		End: protocol.Position{
			Line: int(endLine), Character: int(endCharacter),
		},
	}
}

func renameAdminVueLocal(
	request *lsp.RenameRequest,
) (*protocol.WorkspaceEdit, bool, error) {
	if request == nil || request.Root == nil || request.LineIndex == nil ||
		request.RenameParams == nil ||
		!adminRenameTemplate(request) {
		return nil, false, nil
	}
	offset := request.LineIndex.OffsetUTF16(
		uint32(request.Position.Line), uint32(request.Position.Character),
	)
	binding, found := admin.TwigVueBindingAtOffset(
		request.Root, request.DocumentContent, offset,
	)
	if !found || binding == nil {
		return nil, false, nil
	}
	newName := strings.TrimSpace(request.NewName)
	if newName == binding.Name {
		return &protocol.WorkspaceEdit{}, true, nil
	}
	if binding.Kind == admin.TwigVueBindingEvent {
		return nil, true, fmt.Errorf(
			"cannot rename Vue's implicit $event handler payload",
		)
	}
	if !adminIdentifierPattern.MatchString(newName) {
		return nil, true, fmt.Errorf(
			"%q is not a valid Vue template identifier", newName,
		)
	}
	for _, candidate := range admin.TwigVueBindings(
		request.Root, request.DocumentContent,
	) {
		if candidate.Kind == admin.TwigVueBindingFor &&
			candidate.ScopeRange == binding.ScopeRange &&
			candidate.DeclarationRange != binding.DeclarationRange &&
			candidate.Name == newName {
			return nil, true, fmt.Errorf(
				"vue template identifier %q already exists in this v-for scope",
				newName,
			)
		}
	}
	ranges := admin.TwigVueBindingReferences(
		request.Root, request.DocumentContent, *binding,
	)
	if len(ranges) == 0 {
		return nil, true, nil
	}
	edits := make([]protocol.TextEdit, 0, len(ranges))
	for _, rangeValue := range ranges {
		startLine, startCharacter := request.LineIndex.PositionUTF16(
			rangeValue.Start,
		)
		endLine, endCharacter := request.LineIndex.PositionUTF16(rangeValue.End)
		edits = append(edits, protocol.TextEdit{
			Range: protocol.Range{
				Start: protocol.Position{
					Line: int(startLine), Character: int(startCharacter),
				},
				End: protocol.Position{
					Line: int(endLine), Character: int(endCharacter),
				},
			},
			NewText: newName,
		})
	}
	sort.SliceStable(edits, func(left, right int) bool {
		if edits[left].Range.Start.Line != edits[right].Range.Start.Line {
			return edits[left].Range.Start.Line > edits[right].Range.Start.Line
		}
		return edits[left].Range.Start.Character >
			edits[right].Range.Start.Character
	})
	return &protocol.WorkspaceEdit{Changes: map[string][]protocol.TextEdit{
		request.TextDocument.URI: edits,
	}}, true, nil
}

func hasAdminOwnedSourceOccurrence(
	sets []admin.AdminUsageSet,
	sourcePath string,
) bool {
	for _, set := range sets {
		if filepath.Clean(set.FilePath) == filepath.Clean(sourcePath) &&
			len(set.Occurrences) > 0 {
			return true
		}
	}
	return false
}

func hasAdminDeclarationOccurrence(sets []admin.AdminUsageSet) bool {
	for _, set := range sets {
		for _, occurrence := range set.Occurrences {
			if occurrence.Declaration {
				return true
			}
		}
	}
	return false
}

func adminRenameReplacement(
	target admin.AdminSymbolTarget,
	newName string,
	occurrence admin.AdminSourceRange,
) string {
	replacement := newName
	if occurrence.NameStyle == admin.AdminNameShorthand {
		switch target.Kind {
		case admin.AdminSymbolDirective:
			if occurrence.Declaration {
				return admin.KebabToCamel(newName) + ": " +
					admin.KebabToCamel(target.Name)
			}
		case admin.AdminSymbolComponentProp:
			// `{ title }` forwards the public prop `title` from the private
			// component member `title`. Renaming the prop must retain the
			// expression side explicitly.
			return newName + ": " + target.Name
		case admin.AdminSymbolComponentMember:
			if occurrence.Declaration {
				// JavaScript object shorthand declarations such as
				// `return { title }` need the public member name on the
				// left and must retain the local binding on the right.
				return newName + ": " + target.Name
			}
			// The inverse operation keeps the public object key stable while
			// renaming the component member used as its value.
			return target.Name + ": " + newName
		}
	}
	if target.Kind == admin.AdminSymbolComponentProp &&
		!occurrence.Declaration && !occurrence.Identifier {
		replacement = admin.CamelToKebab(newName)
	}
	if target.Kind == admin.AdminSymbolComponentEvent &&
		occurrence.NameStyle == admin.AdminNameCamel {
		replacement = admin.KebabToCamel(newName)
	}
	if target.Kind == admin.AdminSymbolDirective &&
		occurrence.NameStyle == admin.AdminNameCamel {
		replacement = admin.KebabToCamel(newName)
	}
	if target.Kind == admin.AdminSymbolComponentEvent &&
		occurrence.Identifier &&
		!adminIdentifierPattern.MatchString(replacement) {
		return fmt.Sprintf("%q", replacement)
	}
	return replacement
}

func (p *AdminRenameProvider) targetAt(
	request *lsp.RenameRequest,
) (admin.AdminSymbolTarget, bool, error) {
	ext := strings.ToLower(filepath.Ext(request.TextDocument.URI))
	switch ext {
	case ".js", ".ts":
		path, err := uriutil.Path(request.TextDocument.URI)
		if err != nil {
			return admin.AdminSymbolTarget{}, false, err
		}
		return p.index.JavaScriptSymbolAt(path, request.Node)
	case ".twig":
		if request.LineIndex == nil {
			return admin.AdminSymbolTarget{}, false, nil
		}
		path, err := uriutil.Path(request.TextDocument.URI)
		if err != nil {
			return admin.AdminSymbolTarget{}, false, err
		}
		offset := request.LineIndex.OffsetUTF16(
			uint32(request.Position.Line), uint32(request.Position.Character),
		)
		if _, _, objectProp := admin.TwigComponentObjectBindingFieldAtOffset(
			request.Root, offset,
		); objectProp {
			return p.index.TwigSymbolAt(path, request.Root, offset)
		}
		if target, _, found, err := p.index.TwigComponentMemberAt(
			path, request.Root, request.DocumentContent, offset,
		); found || err != nil {
			return target, found, err
		}
		return p.index.TwigSymbolAt(path, request.Root, offset)
	case ".vue":
		path, err := uriutil.Path(request.TextDocument.URI)
		if err != nil {
			return admin.AdminSymbolTarget{}, false, err
		}
		if adminRenameScript(request) {
			return p.index.JavaScriptSymbolAt(path, request.Node)
		}
		if !adminRenameTemplate(request) || request.LineIndex == nil {
			return admin.AdminSymbolTarget{}, false, nil
		}
		offset := request.LineIndex.OffsetUTF16(
			uint32(request.Position.Line), uint32(request.Position.Character),
		)
		if _, _, objectProp := admin.TwigComponentObjectBindingFieldAtOffset(
			request.Root, offset,
		); objectProp {
			return p.index.TwigSymbolAt(path, request.Root, offset)
		}
		if target, _, found, err := p.index.TwigComponentMemberAt(
			path, request.Root, request.DocumentContent, offset,
		); found || err != nil {
			return target, found, err
		}
		return p.index.TwigSymbolAt(path, request.Root, offset)
	default:
		return admin.AdminSymbolTarget{}, false, nil
	}
}

func adminRenameTemplate(request *lsp.RenameRequest) bool {
	if request == nil || request.TextDocument.URI == "" {
		return false
	}
	ext := strings.ToLower(filepath.Ext(request.TextDocument.URI))
	return ext == ".twig" || ext == ".vue" &&
		lsp.EffectiveSyntaxLanguage(language.Vue, request.Node) == language.Twig
}

func adminRenameScript(request *lsp.RenameRequest) bool {
	if request == nil || request.TextDocument.URI == "" {
		return false
	}
	ext := strings.ToLower(filepath.Ext(request.TextDocument.URI))
	return ext == ".js" || ext == ".ts" || ext == ".vue" &&
		lsp.EffectiveSyntaxLanguage(language.Vue, request.Node) == language.JavaScript
}

func (p *AdminRenameProvider) validateRename(
	target admin.AdminSymbolTarget,
	newName string,
) error {
	if newName == "" || strings.ContainsAny(newName, "'\"`\\\r\n") {
		return fmt.Errorf("%q is not a valid Administration registry name", newName)
	}
	if target.Kind == admin.AdminSymbolComponent &&
		!adminComponentNamePattern.MatchString(newName) {
		return fmt.Errorf("%q is not a valid Administration component name", newName)
	}
	if (target.Kind == admin.AdminSymbolCMSElement ||
		target.Kind == admin.AdminSymbolCMSBlock) &&
		!adminComponentNamePattern.MatchString(newName) {
		return fmt.Errorf("%q is not a valid Shopware CMS registry name", newName)
	}
	if target.Kind == admin.AdminSymbolComponentEvent &&
		!adminEventNamePattern.MatchString(newName) {
		return fmt.Errorf("%q is not a valid Administration component event name", newName)
	}
	if target.Kind == admin.AdminSymbolEventBusEvent &&
		!adminEventNamePattern.MatchString(newName) {
		return fmt.Errorf(
			"%q is not a valid Shopware EventBus event name", newName,
		)
	}
	if target.Kind == admin.AdminSymbolComponentProp &&
		!adminPropNamePattern.MatchString(newName) {
		return fmt.Errorf(
			"%q is not a valid Administration component prop name", newName,
		)
	}
	if target.Kind == admin.AdminSymbolComponentSlot &&
		!adminSlotNamePattern.MatchString(newName) {
		return fmt.Errorf("%q is not a valid Administration component slot name", newName)
	}
	if target.Kind == admin.AdminSymbolDirective &&
		!adminDirectiveNamePattern.MatchString(newName) {
		return fmt.Errorf(
			"%q is not a valid Administration Vue directive name", newName,
		)
	}
	if target.Kind == admin.AdminSymbolComponentMember &&
		!adminIdentifierPattern.MatchString(newName) {
		return fmt.Errorf(
			"%q is not a valid Administration component member name", newName,
		)
	}
	if target.Kind == admin.AdminSymbolComponentSlot {
		dynamic, err := p.index.IsDynamicComponentSlot(target)
		if err != nil {
			return err
		}
		if dynamic {
			return fmt.Errorf(
				"cannot rename Administration component slot %q because it is provided by a dynamic slot family",
				target.Name,
			)
		}
	}
	if (target.Kind == admin.AdminSymbolComponentProp ||
		target.Kind == admin.AdminSymbolComponentEvent ||
		target.Kind == admin.AdminSymbolComponentSlot) &&
		isExternalMeteorAdminPath(target.Owner) {
		return fmt.Errorf(
			"cannot rename external Meteor component %s %s",
			target.Kind,
			target.Name,
		)
	}
	if target.Kind != admin.AdminSymbolComponent {
		return nil
	}
	components, err := p.index.GetComponent(target.Name)
	if err != nil {
		return err
	}
	for _, component := range components {
		if strings.Contains(
			filepath.ToSlash(component.FilePath),
			"/node_modules/@shopware-ag/meteor-component-library/",
		) {
			return fmt.Errorf(
				"cannot rename external Meteor component %s", target.Name,
			)
		}
	}
	return nil
}

func isExternalMeteorAdminPath(path string) bool {
	return strings.Contains(
		filepath.ToSlash(path),
		"/node_modules/@shopware-ag/meteor-component-library/",
	)
}

func (p *AdminRenameProvider) rejectConflict(
	target admin.AdminSymbolTarget,
	newName string,
) error {
	var exists bool
	switch target.Kind {
	case admin.AdminSymbolComponent:
		values, err := p.index.GetComponent(newName)
		if err != nil {
			return err
		}
		exists = len(values) > 0
	case admin.AdminSymbolService:
		values, err := p.index.GetService(newName)
		if err != nil {
			return err
		}
		exists = len(values) > 0
	case admin.AdminSymbolStore:
		values, err := p.index.GetStore(newName)
		if err != nil {
			return err
		}
		exists = len(values) > 0
	case admin.AdminSymbolMixin:
		values, err := p.index.GetMixin(newName)
		if err != nil {
			return err
		}
		exists = len(values) > 0
	case admin.AdminSymbolDirective:
		if target.Owner == "" {
			values, err := p.index.GetDirective(newName)
			if err != nil {
				return err
			}
			exists = len(values) > 0
		} else {
			components, err := p.index.GetComponentsByDefinitionPath(target.Owner)
			if err != nil {
				return err
			}
			for _, component := range components {
				if local, found := component.LocalDirective(newName); found &&
					!strings.EqualFold(local.Name, target.Name) {
					exists = true
					break
				}
			}
		}
	case admin.AdminSymbolFilter:
		values, err := p.index.GetFilter(newName)
		if err != nil {
			return err
		}
		exists = len(values) > 0
	case admin.AdminSymbolCMSElement, admin.AdminSymbolCMSBlock:
		kind := admin.AdminCMSElement
		if target.Kind == admin.AdminSymbolCMSBlock {
			kind = admin.AdminCMSBlock
		}
		values, err := p.index.GetCMSRegistration(kind, newName)
		if err != nil {
			return err
		}
		exists = len(values) > 0
	case admin.AdminSymbolEventBusEvent:
		_, found, err := p.index.ResolveShopwareEventBusEvent(newName, "")
		if err != nil {
			return err
		}
		exists = found
	case admin.AdminSymbolComponentProp,
		admin.AdminSymbolComponentEvent,
		admin.AdminSymbolComponentSlot:
		components, err := p.index.GetComponentsExposingSymbol(target)
		if err != nil {
			return err
		}
		for _, component := range components {
			switch target.Kind {
			case admin.AdminSymbolComponentProp:
				_, exists = component.ComponentProp(newName)
			case admin.AdminSymbolComponentEvent:
				_, exists = component.ComponentEvent(newName)
			case admin.AdminSymbolComponentSlot:
				for _, slot := range component.Slots {
					if slot.Name == newName {
						exists = true
						break
					}
				}
			}
			if exists {
				break
			}
		}
	case admin.AdminSymbolComponentMember:
		components, err := p.index.GetComponentsExposingMember(target)
		if err != nil {
			return err
		}
		for _, component := range components {
			member, found := component.TemplateMember(newName)
			if found && member.SourceIdentity() != target.Owner {
				exists = true
				break
			}
		}
	}
	if exists {
		return fmt.Errorf(
			"administration %s %q already exists", target.Kind, newName,
		)
	}
	return nil
}
