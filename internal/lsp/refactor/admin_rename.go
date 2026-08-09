package refactor

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
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

type adminRenamePlan struct {
	provider      *AdminRenameProvider
	request       *lsp.RenameRequest
	target        admin.AdminSymbolTarget
	newName       string
	sets          []admin.AdminUsageSet
	dynamicRanges []cst.TextRange
	eventBus      admin.TwigVueMember
	changes       map[string][]protocol.TextEdit
	livePath      string
}

func (p *AdminRenameProvider) Rename(
	_ context.Context,
	request *lsp.RenameRequest,
) (*protocol.WorkspaceEdit, error) {
	if p == nil || p.index == nil || request == nil || request.Root == nil {
		return nil, nil
	}
	if edit, handled, err := p.renameLocalSymbol(request); handled || err != nil {
		return edit, err
	}
	target, dynamicRanges, found, err := p.renameTarget(request)
	if err != nil || !found {
		return nil, err
	}
	if target.Kind == admin.AdminSymbolComponentModel {
		return nil, fmt.Errorf(
			"cannot safely rename compound v-model contracts; rename the prop and update event declarations together",
		)
	}
	if !supportedAdminRenameKind(target.Kind) {
		return nil, nil
	}
	newName := normalizedAdminRenameName(target.Kind, request.NewName)
	if newName == target.Name {
		return &protocol.WorkspaceEdit{}, nil
	}
	if err := p.validateRename(target, newName); err != nil {
		return nil, err
	}
	if err := p.rejectConflict(target, newName); err != nil {
		return nil, err
	}
	plan := &adminRenamePlan{
		provider:      p,
		request:       request,
		target:        target,
		newName:       newName,
		dynamicRanges: dynamicRanges,
		changes:       make(map[string][]protocol.TextEdit),
	}
	if err := plan.prepare(); err != nil {
		return nil, err
	}
	if len(plan.sets) == 0 && plan.eventBus.DefinitionPath == "" {
		return nil, nil
	}
	if err := plan.build(); err != nil {
		return nil, err
	}
	return &protocol.WorkspaceEdit{Changes: plan.changes}, nil
}

func (p *AdminRenameProvider) renameLocalSymbol(
	request *lsp.RenameRequest,
) (*protocol.WorkspaceEdit, bool, error) {
	handlers := []func(*lsp.RenameRequest) (*protocol.WorkspaceEdit, bool, error){
		p.renameAdminLocalComponentDeclaration,
		p.renameAdminLocalComponentTag,
		p.renameAdminScopedSlotLocal,
		renameAdminVueLocal,
	}
	for _, handler := range handlers {
		edit, handled, err := handler(request)
		if handled || err != nil {
			return edit, handled, err
		}
	}
	return nil, false, nil
}

func (p *AdminRenameProvider) renameTarget(
	request *lsp.RenameRequest,
) (admin.AdminSymbolTarget, []cst.TextRange, bool, error) {
	target, ranges, handled, err := p.dynamicTwigSlotRenameTarget(request)
	if err != nil {
		return admin.AdminSymbolTarget{}, nil, false, err
	}
	if handled {
		return target, ranges, target.Name != "", nil
	}
	target, found, err := p.targetAt(request)
	return target, nil, found, err
}

func supportedAdminRenameKind(kind admin.AdminSymbolKind) bool {
	switch kind {
	case admin.AdminSymbolComponent,
		admin.AdminSymbolService,
		admin.AdminSymbolStore,
		admin.AdminSymbolMixin,
		admin.AdminSymbolDirective,
		admin.AdminSymbolFilter,
		admin.AdminSymbolCMSElement,
		admin.AdminSymbolCMSBlock,
		admin.AdminSymbolEventBusEvent,
		admin.AdminSymbolComponentProp,
		admin.AdminSymbolComponentEvent,
		admin.AdminSymbolComponentSlot,
		admin.AdminSymbolComponentMember:
		return true
	default:
		return false
	}
}

func normalizedAdminRenameName(kind admin.AdminSymbolKind, value string) string {
	value = strings.TrimSpace(value)
	switch kind {
	case admin.AdminSymbolComponentProp:
		return admin.NormalizePropName(value)
	case admin.AdminSymbolComponentEvent:
		return admin.CanonicalEventName(value)
	default:
		return value
	}
}

func (plan *adminRenamePlan) prepare() error {
	sets, err := plan.provider.index.GetSymbolUsages(plan.target)
	if err != nil {
		return err
	}
	plan.sets = sets
	if err := plan.resolveEventBusDeclaration(); err != nil {
		return err
	}
	if err := plan.rejectCompoundModelUsages(); err != nil {
		return err
	}
	if err := plan.rejectUnsafeDynamicUsages(); err != nil {
		return err
	}
	if err := plan.validateDeclarationSource(); err != nil {
		return err
	}
	return plan.validateServiceIdentifiers()
}

func (plan *adminRenamePlan) resolveEventBusDeclaration() error {
	if plan.target.Kind != admin.AdminSymbolEventBusEvent {
		return nil
	}
	declaration, found, err := plan.provider.index.ResolveShopwareEventBusEvent(
		plan.target.Name,
		"",
	)
	if err != nil {
		return err
	}
	if !found || declaration.DefinitionPath == "" ||
		(!declaration.DefinitionRange.Declaration &&
			!declaration.DefinitionRange.Identifier) {
		return fmt.Errorf(
			"cannot safely rename Shopware EventBus event %q because its typed declaration is not indexed",
			plan.target.Name,
		)
	}
	plan.eventBus = declaration
	return nil
}

func (plan *adminRenamePlan) rejectCompoundModelUsages() error {
	if plan.target.Kind != admin.AdminSymbolComponentProp &&
		plan.target.Kind != admin.AdminSymbolComponentEvent {
		return nil
	}
	for _, set := range plan.sets {
		if set.Kind == admin.AdminSymbolComponentModel {
			return fmt.Errorf(
				"cannot safely rename Administration %s %q because it participates in a compound v-model contract",
				plan.target.Kind,
				plan.target.Name,
			)
		}
	}
	return nil
}

func (plan *adminRenamePlan) rejectUnsafeDynamicUsages() error {
	if plan.target.Kind != admin.AdminSymbolComponentProp &&
		plan.target.Kind != admin.AdminSymbolComponentEvent &&
		plan.target.Kind != admin.AdminSymbolComponentSlot {
		return nil
	}
	for _, set := range plan.sets {
		for _, occurrence := range set.Occurrences {
			safe, err := plan.provider.index.DynamicComponentUsageRenameSafe(
				set,
				occurrence,
				plan.target,
			)
			if err != nil {
				return err
			}
			if !safe {
				return fmt.Errorf(
					"cannot safely rename Administration %s %q because a dynamic component usage resolves to distinct declarations",
					plan.target.Kind,
					plan.target.Name,
				)
			}
		}
	}
	return nil
}

func (plan *adminRenamePlan) validateDeclarationSource() error {
	target := plan.target
	if (target.Kind == admin.AdminSymbolComponentProp ||
		target.Kind == admin.AdminSymbolComponentEvent ||
		target.Kind == admin.AdminSymbolComponentSlot) &&
		!hasAdminOwnedSourceOccurrence(plan.sets, target.Owner) {
		return fmt.Errorf(
			"cannot safely rename Administration %s %q because its declaration source is not indexed",
			target.Kind,
			target.Name,
		)
	}
	if target.Kind == admin.AdminSymbolComponentMember &&
		!hasAdminDeclarationOccurrence(plan.sets) {
		return fmt.Errorf(
			"cannot safely rename Administration component member %q because its declaration source is not indexed",
			target.Name,
		)
	}
	if target.Kind == admin.AdminSymbolDirective && target.Owner != "" &&
		!hasAdminDeclarationOccurrence(plan.sets) {
		return fmt.Errorf(
			"cannot safely rename component-local Administration directive %q because its declaration source is not indexed",
			target.Name,
		)
	}
	if (target.Kind == admin.AdminSymbolCMSElement ||
		target.Kind == admin.AdminSymbolCMSBlock) &&
		!hasAdminDeclarationOccurrence(plan.sets) {
		return fmt.Errorf(
			"cannot safely rename Shopware CMS registry %q because its declaration source is not indexed",
			target.Name,
		)
	}
	return nil
}

func (plan *adminRenamePlan) validateServiceIdentifiers() error {
	if plan.target.Kind != admin.AdminSymbolService ||
		adminIdentifierPattern.MatchString(plan.newName) {
		return nil
	}
	for _, set := range plan.sets {
		for _, occurrence := range set.Occurrences {
			if occurrence.Identifier {
				return fmt.Errorf(
					"%q is not a valid JavaScript identifier for injected service %s",
					plan.newName,
					plan.target.Name,
				)
			}
		}
	}
	return nil
}

func (plan *adminRenamePlan) build() error {
	plan.addEventBusDeclaration()
	plan.addDynamicSlotRanges()
	if err := plan.addLiveTemplateMembers(); err != nil {
		return err
	}
	plan.addIndexedOccurrences()
	plan.normalizeEdits()
	return nil
}

func (plan *adminRenamePlan) addEventBusDeclaration() {
	declaration := plan.eventBus
	if declaration.DefinitionPath == "" {
		return
	}
	uri := uriutil.FileURI(declaration.DefinitionPath)
	plan.changes[uri] = append(plan.changes[uri], protocol.TextEdit{
		Range: protocol.Range{
			Start: protocol.Position{
				Line: declaration.DefinitionRange.StartLine, Character: declaration.DefinitionRange.StartCharacter,
			},
			End: protocol.Position{
				Line: declaration.DefinitionRange.EndLine, Character: declaration.DefinitionRange.EndCharacter,
			},
		},
		NewText: plan.newName,
	})
}

func (plan *adminRenamePlan) addDynamicSlotRanges() {
	for _, rangeValue := range plan.dynamicRanges {
		uri := plan.request.TextDocument.URI
		plan.changes[uri] = append(plan.changes[uri], protocol.TextEdit{
			Range:   adminRenameProtocolRange(plan.request.LineIndex, rangeValue),
			NewText: plan.newName,
		})
	}
}

func (plan *adminRenamePlan) addLiveTemplateMembers() error {
	if plan.target.Kind != admin.AdminSymbolComponentMember ||
		!adminRenameTemplate(plan.request) {
		return nil
	}
	path, err := uriutil.Path(plan.request.TextDocument.URI)
	if err != nil {
		return err
	}
	plan.livePath = path
	for _, identifier := range admin.TwigVueExpressionRootIdentifiers(
		plan.request.Root,
		plan.request.DocumentContent,
	) {
		candidate, _, found, err := plan.provider.index.TwigComponentMemberAt(
			path,
			plan.request.Root,
			plan.request.DocumentContent,
			identifier.Range.Start,
		)
		if err != nil {
			return err
		}
		if !found || candidate != plan.target {
			continue
		}
		uri := plan.request.TextDocument.URI
		plan.changes[uri] = append(plan.changes[uri], protocol.TextEdit{
			Range:   adminRenameProtocolRange(plan.request.LineIndex, identifier.Range),
			NewText: plan.newName,
		})
	}
	return nil
}

func (plan *adminRenamePlan) addIndexedOccurrences() {
	for _, set := range plan.sets {
		if plan.livePath != "" && filepath.Clean(set.FilePath) == filepath.Clean(plan.livePath) {
			continue
		}
		uri := uriutil.FileURI(set.FilePath)
		for _, occurrence := range set.Occurrences {
			plan.changes[uri] = append(plan.changes[uri], protocol.TextEdit{
				Range: protocol.Range{
					Start: protocol.Position{Line: occurrence.StartLine, Character: occurrence.StartCharacter},
					End:   protocol.Position{Line: occurrence.EndLine, Character: occurrence.EndCharacter},
				},
				NewText: adminRenameReplacement(plan.target, plan.newName, occurrence),
			})
		}
	}
}

func (plan *adminRenamePlan) normalizeEdits() {
	for uri, edits := range plan.changes {
		seen := make(map[string]bool, len(edits))
		unique := edits[:0]
		for _, edit := range edits {
			key := fmt.Sprintf(
				"%d:%d:%d:%d:%s",
				edit.Range.Start.Line,
				edit.Range.Start.Character,
				edit.Range.End.Line,
				edit.Range.End.Character,
				edit.NewText,
			)
			if seen[key] {
				continue
			}
			seen[key] = true
			unique = append(unique, edit)
		}
		plan.changes[uri] = unique
		sort.SliceStable(plan.changes[uri], func(left, right int) bool {
			leftStart := plan.changes[uri][left].Range.Start
			rightStart := plan.changes[uri][right].Range.Start
			if leftStart.Line != rightStart.Line {
				return leftStart.Line > rightStart.Line
			}
			return leftStart.Character > rightStart.Character
		})
	}
}
