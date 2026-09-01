package reference

import (
	"context"
	"fmt"
	"path/filepath"
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

// AdminReferenceProvider exposes the cached cross-language usage graph for
// Administration registry symbols.
type AdminReferenceProvider struct {
	index *admin.AdminComponentIndexer
}

func NewAdminReferenceProvider(
	index *admin.AdminComponentIndexer,
) *AdminReferenceProvider {
	return &AdminReferenceProvider{index: index}
}

func (p *AdminReferenceProvider) GetReferences(
	_ context.Context,
	params *lsp.ReferenceRequest,
) ([]protocol.Location, error) {
	if p == nil || p.index == nil || params == nil || params.Root == nil {
		return nil, nil
	}
	if locations, handled, err := p.adminScopedSlotMemberReferences(params); handled || err != nil {
		return locations, err
	}
	if locations, handled, err := p.adminScopedSlotLocalReferences(params); handled || err != nil {
		return locations, err
	}
	if locations, handled := adminVueLocalReferences(params); handled {
		return locations, nil
	}
	if locations, handled, err := p.adminComponentObjectBindingReferences(
		params,
	); handled || err != nil {
		return locations, err
	}
	if locations, handled, err := p.adminVueInstanceRootReferences(
		params,
	); handled || err != nil {
		return locations, err
	}
	if locations, handled, err := p.adminVueInstanceMemberReferences(
		params,
	); handled || err != nil {
		return locations, err
	}
	if locations, handled, err := p.adminLocalComponentTagReferences(
		params,
	); handled || err != nil {
		return locations, err
	}
	if locations, handled, err := p.adminDynamicComponentSlotReferences(
		params,
	); handled || err != nil {
		return locations, err
	}
	if locations, handled, err := p.adminDynamicComponentAttributeReferences(
		params,
	); handled || err != nil {
		return locations, err
	}
	if locations, handled, err := p.adminComponentModelReferences(
		params,
	); handled || err != nil {
		return locations, err
	}
	target, found, targetErr := p.targetAt(params)
	if targetErr != nil {
		return nil, targetErr
	}
	if !found {
		return nil, nil
	}
	return p.symbolReferences(params, []admin.AdminSymbolTarget{target})
}

func (p *AdminReferenceProvider) adminScopedSlotLocalReferences(
	params *lsp.ReferenceRequest,
) ([]protocol.Location, bool, error) {
	if p == nil || p.index == nil || params == nil || params.Root == nil ||
		params.Node == nil || params.LineIndex == nil ||
		params.ReferenceParams == nil ||
		!adminReferenceTemplate(params) {
		return nil, false, nil
	}
	offset := params.LineIndex.OffsetUTF16(
		uint32(params.Position.Line), uint32(params.Position.Character),
	)
	templatePath, err := uriutil.Path(params.TextDocument.URI)
	if err != nil {
		return nil, false, err
	}
	liveOwner, err := p.index.GetComponentForDocument(
		templatePath, params.Root, params.SourceString(), params.LineIndex,
	)
	if err != nil {
		return nil, false, err
	}
	resolved, err := p.index.ResolveTwigScopedSlotBindingForOwner(
		params.Root, params.Node, params.DocumentContent, offset,
		templatePath, liveOwner,
	)
	if err != nil || resolved == nil {
		return nil, resolved != nil, err
	}
	if resolved.Scope.IsBindingOffset(offset) &&
		resolved.Identifier == resolved.Binding.MemberName &&
		resolved.Binding.MemberName != resolved.Binding.LocalName {
		return nil, true, nil
	}
	ranges := admin.TwigScopedSlotBindingReferences(
		params.Root, params.DocumentContent, *resolved,
	)
	if !params.Context.IncludeDeclaration {
		filtered := ranges[:0]
		for _, rangeValue := range ranges {
			if rangeValue != resolved.Binding.LocalRange {
				filtered = append(filtered, rangeValue)
			}
		}
		ranges = filtered
	}
	return adminLocalRangesToLocations(params, ranges), true, nil
}

func (p *AdminReferenceProvider) adminDynamicComponentSlotReferences(
	params *lsp.ReferenceRequest,
) ([]protocol.Location, bool, error) {
	if p == nil || p.index == nil || params == nil || params.Root == nil ||
		params.LineIndex == nil || params.ReferenceParams == nil ||
		!adminReferenceTemplate(params) {
		return nil, false, nil
	}
	offset := params.LineIndex.OffsetUTF16(
		uint32(params.Position.Line), uint32(params.Position.Character),
	)
	attribute := twigquery.HTMLAttributeAt(params.Root.NodeAtOffset(offset))
	if attribute == nil {
		return nil, false, nil
	}
	attributeName := twigquery.HTMLAttributeName(attribute)
	if admin.NormalizeSlotName(attributeName) == "" {
		return nil, false, nil
	}
	startTag := twigquery.StartingHTMLTagAt(attribute)
	owner := admin.TwigSlotOwnerStartingTag(startTag)
	if _, dynamic := admin.TwigDynamicComponentSelector(owner); !dynamic {
		return nil, false, nil
	}
	attributeNode, ok := twigast.CastHtmlAttribute(attribute)
	if !ok || attributeNode.Name() == nil {
		return nil, true, nil
	}
	reference, found := admin.VueSlotReferenceForAttribute(
		attributeName, attributeNode.Name().Range(),
	)
	if !found || offset < reference.Range.Start || offset > reference.Range.End {
		return nil, true, nil
	}
	templatePath, err := uriutil.Path(params.TextDocument.URI)
	if err != nil {
		return nil, true, err
	}
	liveOwner, err := p.index.GetComponentForDocument(
		templatePath, params.Root, params.SourceString(), params.LineIndex,
	)
	if err != nil {
		return nil, true, err
	}
	components, complete, err :=
		p.index.ResolveTwigSlotConsumerComponents(
			templatePath, startTag, liveOwner,
		)
	if err != nil || !complete {
		return nil, true, err
	}
	targets := dynamicComponentAttributeTargets(
		components, attributeName,
	)
	if len(targets) == 0 {
		return nil, true, nil
	}
	locations, err := p.symbolReferences(params, targets)
	if err != nil {
		return nil, true, err
	}
	local, err := p.dynamicComponentSlotLocations(
		params, templatePath, liveOwner, targets,
	)
	if err != nil {
		return nil, true, err
	}
	return mergeAdminReferenceLocations(locations, local), true, nil
}

func (p *AdminReferenceProvider) adminComponentObjectBindingReferences(
	params *lsp.ReferenceRequest,
) ([]protocol.Location, bool, error) {
	if p == nil || p.index == nil || params == nil || params.Root == nil ||
		params.LineIndex == nil || params.ReferenceParams == nil ||
		!adminReferenceTemplate(params) {
		return nil, false, nil
	}
	offset := params.LineIndex.OffsetUTF16(
		uint32(params.Position.Line), uint32(params.Position.Character),
	)
	startTag, field, found := admin.TwigComponentObjectBindingFieldAtOffset(
		params.Root, offset,
	)
	if !found {
		return nil, false, nil
	}
	templatePath, err := uriutil.Path(params.TextDocument.URI)
	if err != nil {
		return nil, true, err
	}
	liveOwner, err := p.index.GetComponentForDocument(
		templatePath, params.Root, params.SourceString(), params.LineIndex,
	)
	if err != nil {
		return nil, true, err
	}
	selector, dynamic := admin.TwigDynamicComponentSelector(startTag)
	if dynamic {
		_, components, complete, resolveErr :=
			p.index.ResolveDynamicComponentContractsForOwner(
				templatePath, selector, liveOwner, startTag,
			)
		if resolveErr != nil || !complete {
			return nil, true, resolveErr
		}
		targets := dynamicComponentAttributeTargets(components, field.Name)
		locations, referenceErr := p.symbolReferences(params, targets)
		if referenceErr != nil {
			return nil, true, referenceErr
		}
		local, localErr := p.dynamicComponentAttributeLocations(
			params, templatePath, liveOwner, targets,
		)
		if localErr != nil {
			return nil, true, localErr
		}
		return mergeAdminReferenceLocations(locations, local), true, nil
	}
	target, found, targetErr := p.index.TwigSymbolAt(
		templatePath, params.Root, offset,
	)
	if targetErr != nil || !found {
		return nil, true, targetErr
	}
	locations, referenceErr := p.symbolReferences(
		params, []admin.AdminSymbolTarget{target},
	)
	return locations, true, referenceErr
}

func (p *AdminReferenceProvider) adminDynamicComponentAttributeReferences(
	params *lsp.ReferenceRequest,
) ([]protocol.Location, bool, error) {
	if p == nil || p.index == nil || params == nil || params.Root == nil ||
		params.LineIndex == nil || params.ReferenceParams == nil ||
		!adminReferenceTemplate(params) {
		return nil, false, nil
	}
	offset := params.LineIndex.OffsetUTF16(
		uint32(params.Position.Line), uint32(params.Position.Character),
	)
	attribute := twigquery.HTMLAttributeAt(params.Root.NodeAtOffset(offset))
	startTag := twigquery.StartingHTMLTagAt(attribute)
	selector, dynamic := admin.TwigDynamicComponentSelector(startTag)
	if !dynamic || attribute == nil ||
		twigquery.HTMLAttributeName(attribute) == selector.AttributeName {
		return nil, false, nil
	}
	templatePath, err := uriutil.Path(params.TextDocument.URI)
	if err != nil {
		return nil, true, err
	}
	liveOwner, err := p.index.GetComponentForDocument(
		templatePath, params.Root, params.SourceString(), params.LineIndex,
	)
	if err != nil {
		return nil, true, err
	}
	_, components, complete, err :=
		p.index.ResolveDynamicComponentContractsForOwner(
			templatePath, selector, liveOwner, startTag,
		)
	if err != nil || !complete {
		return nil, complete, err
	}
	attributeName := twigquery.HTMLAttributeName(attribute)
	targets := dynamicComponentAttributeTargets(components, attributeName)
	if len(targets) == 0 {
		return nil, true, nil
	}
	locations, err := p.symbolReferences(params, targets)
	if err != nil {
		return nil, true, err
	}
	local, err := p.dynamicComponentAttributeLocations(
		params, templatePath, liveOwner, targets,
	)
	if err != nil {
		return nil, true, err
	}
	return mergeAdminReferenceLocations(locations, local), true, nil
}

func dynamicComponentAttributeTargets(
	components []admin.VueComponent,
	attributeName string,
) []admin.AdminSymbolTarget {
	var result []admin.AdminSymbolTarget
	seen := make(map[admin.AdminSymbolTarget]bool)
	add := func(target admin.AdminSymbolTarget) {
		if target.Name == "" || target.Owner == "" || seen[target] {
			return
		}
		seen[target] = true
		result = append(result, target)
	}
	for _, component := range components {
		if _, model := admin.NormalizeModelArgument(attributeName); model {
			binding, found := component.ComponentModel(attributeName)
			if !found {
				continue
			}
			add(adminComponentPropReferenceTarget(component, binding.Prop))
			add(adminComponentEventReferenceTarget(component, binding.Event))
			continue
		}
		if eventName := admin.NormalizeEventName(attributeName); eventName != "" {
			event, found := component.ComponentEvent(eventName)
			if found {
				add(adminComponentEventReferenceTarget(component, event))
			}
			continue
		}
		if slotName := admin.NormalizeSlotName(attributeName); slotName != "" {
			slot, found := component.ComponentSlot(slotName)
			if found {
				owner := slot.FilePath
				if owner == "" {
					owner = component.TemplatePath
				}
				add(admin.AdminSymbolTarget{
					Kind:  admin.AdminSymbolComponentSlot,
					Owner: owner,
					Name:  slotName,
				})
			}
			continue
		}
		propName := admin.NormalizePropName(attributeName)
		prop, found := component.ComponentProp(propName)
		if found {
			add(adminComponentPropReferenceTarget(component, prop))
		}
	}
	return result
}

func adminComponentPropReferenceTarget(
	component admin.VueComponent,
	prop admin.VueComponentProp,
) admin.AdminSymbolTarget {
	owner := prop.FilePath
	if owner == "" {
		owner = component.DefinitionPath
	}
	if owner == "" {
		owner = component.FilePath
	}
	return admin.AdminSymbolTarget{
		Kind: admin.AdminSymbolComponentProp, Owner: owner, Name: prop.Name,
	}
}

func adminComponentEventReferenceTarget(
	component admin.VueComponent,
	event admin.VueComponentEvent,
) admin.AdminSymbolTarget {
	owner := event.FilePath
	if owner == "" {
		owner = component.DefinitionPath
	}
	if owner == "" {
		owner = component.FilePath
	}
	return admin.AdminSymbolTarget{
		Kind: admin.AdminSymbolComponentEvent, Owner: owner,
		Name: admin.CanonicalEventName(event.Name),
	}
}

func (p *AdminReferenceProvider) dynamicComponentAttributeLocations(
	params *lsp.ReferenceRequest,
	templatePath string,
	liveOwner *admin.VueComponent,
	targets []admin.AdminSymbolTarget,
) ([]protocol.Location, error) {
	targetSet := make(map[admin.AdminSymbolTarget]bool, len(targets))
	for _, target := range targets {
		targetSet[target] = true
	}
	var ranges []cst.TextRange
	for _, startTag := range twigquery.Nodes(
		params.Root, twigsyntax.HtmlStartingTag,
	) {
		selector, dynamic := admin.TwigDynamicComponentSelector(startTag)
		if !dynamic {
			continue
		}
		_, components, complete, err :=
			p.index.ResolveDynamicComponentContractsForOwner(
				templatePath, selector, liveOwner, startTag,
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
		for attribute := range tag.Attributes() {
			nameToken := attribute.Name()
			if nameToken == nil {
				continue
			}
			name := twigquery.HTMLAttributeName(attribute.Syntax())
			if name == selector.AttributeName {
				continue
			}
			if name == "v-bind" {
				if value, valueOK := attribute.Value(); valueOK {
					if inner, innerOK := value.GetInner(); innerOK {
						fields, _ := admin.VueObjectBindingFields(
							inner.Syntax().Text(), inner.Syntax().Range().Start,
						)
						for _, field := range fields {
							for _, target := range dynamicComponentAttributeTargets(
								components, field.Name,
							) {
								if targetSet[target] {
									ranges = append(ranges, field.NameRange)
									break
								}
							}
						}
					}
				}
			}
			for _, target := range dynamicComponentAttributeTargets(components, name) {
				if targetSet[target] {
					ranges = append(ranges, nameToken.Range())
					break
				}
			}
		}
	}
	return adminLocalRangesToLocations(params, ranges), nil
}

func (p *AdminReferenceProvider) dynamicComponentSlotLocations(
	params *lsp.ReferenceRequest,
	templatePath string,
	liveOwner *admin.VueComponent,
	targets []admin.AdminSymbolTarget,
) ([]protocol.Location, error) {
	targetSet := make(map[admin.AdminSymbolTarget]bool, len(targets))
	for _, target := range targets {
		targetSet[target] = true
	}
	var ranges []cst.TextRange
	for _, startTag := range twigquery.Nodes(
		params.Root, twigsyntax.HtmlStartingTag,
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
		for attribute := range tag.Attributes() {
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
			for _, target := range dynamicComponentAttributeTargets(
				components, name,
			) {
				if targetSet[target] {
					ranges = append(ranges, reference.Range)
					break
				}
			}
		}
	}
	return adminLocalRangesToLocations(params, ranges), nil
}

func mergeAdminReferenceLocations(
	groups ...[]protocol.Location,
) []protocol.Location {
	var result []protocol.Location
	seen := make(map[string]bool)
	for _, group := range groups {
		for _, location := range group {
			key := fmt.Sprintf(
				"%s:%d:%d:%d:%d", location.URI,
				location.Range.Start.Line, location.Range.Start.Character,
				location.Range.End.Line, location.Range.End.Character,
			)
			if seen[key] {
				continue
			}
			seen[key] = true
			result = append(result, location)
		}
	}
	sort.SliceStable(result, func(left, right int) bool {
		if result[left].URI != result[right].URI {
			return result[left].URI < result[right].URI
		}
		if result[left].Range.Start.Line != result[right].Range.Start.Line {
			return result[left].Range.Start.Line < result[right].Range.Start.Line
		}
		return result[left].Range.Start.Character < result[right].Range.Start.Character
	})
	return result
}

func (p *AdminReferenceProvider) adminComponentModelReferences(
	params *lsp.ReferenceRequest,
) ([]protocol.Location, bool, error) {
	if params == nil || params.Root == nil || params.LineIndex == nil ||
		!adminReferenceTemplate(params) {
		return nil, false, nil
	}
	offset := params.LineIndex.OffsetUTF16(
		uint32(params.Position.Line), uint32(params.Position.Character),
	)
	raw, found := admin.TwigSymbolAtOffset(params.Root, offset)
	if !found || raw.Kind != admin.AdminSymbolComponentModel {
		return nil, false, nil
	}
	path, err := uriutil.Path(params.TextDocument.URI)
	if err != nil {
		return nil, true, err
	}
	liveOwner, err := p.index.GetComponentForDocument(
		path, params.Root, params.SourceString(), params.LineIndex,
	)
	if err != nil {
		return nil, true, err
	}
	component, found, err := p.index.GetComponentForTemplateTag(
		path, raw.Owner, liveOwner,
	)
	if err != nil || !found || component == nil {
		return nil, true, err
	}
	binding, found := component.ComponentModel(raw.Name)
	if !found {
		return nil, true, nil
	}
	propOwner := binding.Prop.FilePath
	if propOwner == "" {
		propOwner = component.DefinitionPath
	}
	if propOwner == "" {
		propOwner = component.FilePath
	}
	eventOwner := binding.Event.FilePath
	if eventOwner == "" {
		eventOwner = component.DefinitionPath
	}
	if eventOwner == "" {
		eventOwner = component.FilePath
	}
	targets := make([]admin.AdminSymbolTarget, 0, 2)
	if propOwner != "" {
		targets = append(targets, admin.AdminSymbolTarget{
			Kind: admin.AdminSymbolComponentProp, Owner: propOwner,
			Name: binding.PropName,
		})
	}
	if eventOwner != "" {
		targets = append(targets, admin.AdminSymbolTarget{
			Kind: admin.AdminSymbolComponentEvent, Owner: eventOwner,
			Name: binding.EventName,
		})
	}
	locations, err := p.symbolReferences(params, targets)
	return locations, true, err
}

func (p *AdminReferenceProvider) symbolReferences(
	params *lsp.ReferenceRequest,
	targets []admin.AdminSymbolTarget,
) ([]protocol.Location, error) {
	includeDeclaration := params.Context.IncludeDeclaration
	livePath, liveSets, err := adminLiveUsageSets(params)
	if err != nil {
		return nil, err
	}
	locations := make([]protocol.Location, 0)
	seen := make(map[string]bool)
	add := func(location protocol.Location) {
		key := fmt.Sprintf(
			"%s:%d:%d:%d:%d", location.URI,
			location.Range.Start.Line, location.Range.Start.Character,
			location.Range.End.Line, location.Range.End.Character,
		)
		if seen[key] {
			return
		}
		seen[key] = true
		locations = append(locations, location)
	}
	for _, target := range targets {
		sets, err := p.usageSets(target)
		if err != nil {
			return nil, err
		}
		hasDeclaration := false
		for _, set := range sets {
			if livePath != "" && normalizeAdminReferencePath(set.FilePath) ==
				normalizeAdminReferencePath(livePath) {
				live, found := liveSets[admin.AdminUsageKey(
					set.Kind, set.Owner, set.Name,
				)]
				if !found {
					continue
				}
				set = live
			}
			for _, occurrence := range set.Occurrences {
				if occurrence.Declaration {
					hasDeclaration = true
					if !includeDeclaration {
						continue
					}
				}
				add(adminUsageLocation(set.FilePath, occurrence))
			}
		}
		if includeDeclaration && !hasDeclaration {
			declarations, declarationErr := p.declarationLocations(target)
			if declarationErr != nil {
				return nil, declarationErr
			}
			for _, declaration := range declarations {
				if livePath != "" && adminReferenceURIPathEqual(
					declaration.URI, livePath,
				) {
					continue
				}
				add(declaration)
			}
		}
	}
	sort.SliceStable(locations, func(left, right int) bool {
		if locations[left].URI != locations[right].URI {
			return locations[left].URI < locations[right].URI
		}
		if locations[left].Range.Start.Line != locations[right].Range.Start.Line {
			return locations[left].Range.Start.Line < locations[right].Range.Start.Line
		}
		return locations[left].Range.Start.Character <
			locations[right].Range.Start.Character
	})
	return locations, nil
}

func adminLiveUsageSets(
	params *lsp.ReferenceRequest,
) (string, map[string]admin.AdminUsageSet, error) {
	if params == nil || params.Document == nil || params.Root == nil ||
		params.LineIndex == nil {
		return "", nil, nil
	}
	filePath, err := uriutil.Path(params.TextDocument.URI)
	if err != nil {
		return "", nil, err
	}
	var sets []admin.AdminUsageSet
	switch strings.ToLower(filepath.Ext(filePath)) {
	case ".js", ".ts":
		sets = admin.CollectJavaScriptUsages(
			params.Root, filePath, params.LineIndex,
		)
	case ".twig":
		sets = admin.CollectTwigUsages(
			params.Root, filePath, params.LineIndex,
		)
	case ".vue":
		sets = admin.CollectJavaScriptUsages(
			params.Root, filePath, params.LineIndex,
		)
		sets = append(
			sets,
			admin.CollectTwigUsages(params.Root, filePath, params.LineIndex)...,
		)
	default:
		return "", nil, nil
	}
	result := make(map[string]admin.AdminUsageSet, len(sets))
	for _, set := range sets {
		result[admin.AdminUsageKey(set.Kind, set.Owner, set.Name)] = set
	}
	return filePath, result, nil
}

func adminReferenceURIPathEqual(uri, path string) bool {
	resolved, err := uriutil.Path(uri)
	return err == nil && normalizeAdminReferencePath(resolved) ==
		normalizeAdminReferencePath(path)
}

func (p *AdminReferenceProvider) adminVueInstanceRootReferences(
	params *lsp.ReferenceRequest,
) ([]protocol.Location, bool, error) {
	if p == nil || p.index == nil || params == nil || params.Root == nil ||
		params.LineIndex == nil || params.ReferenceParams == nil ||
		!adminReferenceTemplate(params) {
		return nil, false, nil
	}
	offset := params.LineIndex.OffsetUTF16(
		uint32(params.Position.Line), uint32(params.Position.Character),
	)
	templatePath, err := uriutil.Path(params.TextDocument.URI)
	if err != nil {
		return nil, false, err
	}
	target, member, found, err := p.index.TwigComponentMemberAt(
		templatePath, params.Root, params.DocumentContent, offset,
	)
	if err != nil || !found {
		return nil, false, nil
	}
	sets, err := p.index.GetSymbolUsages(target)
	if err != nil {
		return nil, true, err
	}
	var ranges []cst.TextRange
	for _, identifier := range admin.TwigVueExpressionRootIdentifiers(
		params.Root, params.DocumentContent,
	) {
		candidate, _, candidateFound, resolveErr := p.index.TwigComponentMemberAt(
			templatePath,
			params.Root,
			params.DocumentContent,
			identifier.Range.Start,
		)
		if resolveErr != nil {
			return nil, true, resolveErr
		}
		if candidateFound && candidate == target {
			ranges = append(ranges, identifier.Range)
		}
	}
	locations := adminLocalRangesToLocations(params, ranges)
	seenDeclaration := false
	for _, set := range sets {
		if normalizeAdminReferencePath(set.FilePath) ==
			normalizeAdminReferencePath(templatePath) {
			continue
		}
		for _, occurrence := range set.Occurrences {
			if occurrence.Declaration {
				seenDeclaration = true
				if !params.Context.IncludeDeclaration {
					continue
				}
			}
			locations = append(
				locations, adminUsageLocation(set.FilePath, occurrence),
			)
		}
	}
	if params.Context.IncludeDeclaration && !seenDeclaration &&
		member.FilePath != "" {
		var declarationRange protocol.Range
		if member.Renameable() {
			declarationRange = protocol.Range{
				Start: protocol.Position{
					Line:      member.NameRange.StartLine,
					Character: member.NameRange.StartCharacter,
				},
				End: protocol.Position{
					Line:      member.NameRange.EndLine,
					Character: member.NameRange.EndCharacter,
				},
			}
		} else {
			line := member.Line
			if line < 1 {
				line = 1
			}
			declarationRange = protocol.Range{
				Start: protocol.Position{Line: line - 1},
				End:   protocol.Position{Line: line - 1},
			}
		}
		locations = append(locations, protocol.Location{
			URI: uriutil.FileURI(member.FilePath), Range: declarationRange,
		})
	}
	sort.SliceStable(locations, func(left, right int) bool {
		if locations[left].URI != locations[right].URI {
			return locations[left].URI < locations[right].URI
		}
		if locations[left].Range.Start.Line != locations[right].Range.Start.Line {
			return locations[left].Range.Start.Line < locations[right].Range.Start.Line
		}
		return locations[left].Range.Start.Character <
			locations[right].Range.Start.Character
	})
	return locations, true, nil
}

func normalizeAdminReferencePath(value string) string {
	if value == "" {
		return ""
	}
	return filepath.Clean(value)
}

func (p *AdminReferenceProvider) adminLocalComponentTagReferences(
	params *lsp.ReferenceRequest,
) ([]protocol.Location, bool, error) {
	if p == nil || p.index == nil || params == nil || params.Root == nil ||
		params.LineIndex == nil || params.ReferenceParams == nil {
		return nil, false, nil
	}
	ext := strings.ToLower(filepath.Ext(params.TextDocument.URI))
	if ext == ".js" || ext == ".ts" ||
		ext == ".vue" && adminReferenceScript(params) {
		return p.adminLocalComponentDefinitionReferences(params)
	}
	if ext != ".twig" && (ext != ".vue" || !adminReferenceTemplate(params)) {
		return nil, false, nil
	}
	templatePath, err := uriutil.Path(params.TextDocument.URI)
	if err != nil {
		return nil, false, err
	}
	offset := params.LineIndex.OffsetUTF16(
		uint32(params.Position.Line), uint32(params.Position.Character),
	)
	target, found := admin.TwigSymbolAtOffset(params.Root, offset)
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
	var ranges []cst.TextRange
	for _, node := range twigquery.Nodes(
		params.Root,
		twigsyntax.HtmlStartingTag,
		twigsyntax.HtmlEndingTag,
	) {
		switch node.Kind() {
		case twigsyntax.HtmlStartingTag:
			tag, ok := twigast.CastHtmlStartingTag(node)
			if ok && tag.Name() != nil && tag.Name().Text() == local.Name {
				ranges = append(ranges, tag.Name().Range())
			}
			if selector, dynamic := admin.TwigDynamicComponentSelector(node); dynamic {
				for _, candidate := range selector.Candidates {
					if candidate.Name == local.Name {
						ranges = append(ranges, candidate.Range)
					}
				}
			}
		case twigsyntax.HtmlEndingTag:
			tag, ok := twigast.CastHtmlEndingTag(node)
			if ok && tag.Name() != nil && tag.Name().Text() == local.Name {
				ranges = append(ranges, tag.Name().Range())
			}
		}
	}
	locations := adminLocalRangesToLocations(params, ranges)
	if params.Context.IncludeDeclaration && local.FilePath != "" {
		locations = append(locations, adminLocalComponentDeclaration(local))
	}
	sort.SliceStable(locations, func(left, right int) bool {
		if locations[left].URI != locations[right].URI {
			return locations[left].URI < locations[right].URI
		}
		if locations[left].Range.Start.Line != locations[right].Range.Start.Line {
			return locations[left].Range.Start.Line < locations[right].Range.Start.Line
		}
		return locations[left].Range.Start.Character <
			locations[right].Range.Start.Character
	})
	return locations, true, nil
}

func (p *AdminReferenceProvider) adminLocalComponentDefinitionReferences(
	params *lsp.ReferenceRequest,
) ([]protocol.Location, bool, error) {
	definitionPath, err := uriutil.Path(params.TextDocument.URI)
	if err != nil {
		return nil, false, err
	}
	owner, local, found, err := p.index.GetLocalComponentAtDefinitionPosition(
		definitionPath,
		params.Position.Line,
		params.Position.Character,
	)
	if err != nil || !found || owner == nil {
		return nil, false, err
	}
	sets, err := p.index.GetUsages(
		admin.AdminSymbolComponent, "", local.Name,
	)
	if err != nil {
		return nil, true, err
	}
	var locations []protocol.Location
	for _, set := range sets {
		if filepath.Clean(set.FilePath) != filepath.Clean(owner.TemplatePath) {
			continue
		}
		for _, occurrence := range set.Occurrences {
			locations = append(
				locations, adminUsageLocation(set.FilePath, occurrence),
			)
		}
	}
	if params.Context.IncludeDeclaration {
		locations = append(locations, adminLocalComponentDeclaration(local))
	}
	sort.SliceStable(locations, func(left, right int) bool {
		if locations[left].URI != locations[right].URI {
			return locations[left].URI < locations[right].URI
		}
		if locations[left].Range.Start.Line != locations[right].Range.Start.Line {
			return locations[left].Range.Start.Line < locations[right].Range.Start.Line
		}
		return locations[left].Range.Start.Character <
			locations[right].Range.Start.Character
	})
	return locations, true, nil
}

func adminLocalComponentDeclaration(
	local admin.VueLocalComponent,
) protocol.Location {
	rangeValue := protocol.Range{
		Start: protocol.Position{
			Line:      local.NameRange.StartLine,
			Character: local.NameRange.StartCharacter,
		},
		End: protocol.Position{
			Line:      local.NameRange.EndLine,
			Character: local.NameRange.EndCharacter,
		},
	}
	if local.NameRange.EndLine == 0 &&
		local.NameRange.EndCharacter == 0 && local.Line > 1 {
		rangeValue.Start.Line = local.Line - 1
		rangeValue.End.Line = local.Line - 1
	}
	return protocol.Location{
		URI: uriutil.FileURI(local.FilePath), Range: rangeValue,
	}
}

func (p *AdminReferenceProvider) adminScopedSlotMemberReferences(
	params *lsp.ReferenceRequest,
) ([]protocol.Location, bool, error) {
	if params == nil || params.Root == nil || params.Node == nil ||
		params.LineIndex == nil || params.ReferenceParams == nil ||
		!adminReferenceTemplate(params) {
		return nil, false, nil
	}
	offset := params.LineIndex.OffsetUTF16(
		uint32(params.Position.Line), uint32(params.Position.Character),
	)
	access, found := admin.TwigVueExpressionMemberAtOffset(
		params.Root, params.DocumentContent, offset,
	)
	if !found || access.Member == "" {
		return nil, false, nil
	}
	templatePath, pathErr := uriutil.Path(params.TextDocument.URI)
	if pathErr != nil {
		return nil, true, pathErr
	}
	liveOwner, err := p.index.GetComponentForDocument(
		templatePath, params.Root, params.SourceString(), params.LineIndex,
	)
	if err != nil {
		return nil, true, err
	}
	resolved, err := p.index.ResolveTwigScopedSlotMemberForOwner(
		params.Root, params.Node, params.DocumentContent, offset,
		templatePath, liveOwner,
	)
	if err != nil || resolved == nil {
		return nil, resolved != nil, err
	}
	if vueBinding, vueFound := admin.TwigVueBindingAtOffset(
		params.Root, params.DocumentContent, access.RootRange.Start,
	); vueFound && vueBinding != nil &&
		vueBinding.ScopeRange.Len() <= resolved.Scope.TemplateRange.Len() {
		return nil, false, nil
	}
	ranges := admin.TwigScopedSlotMemberReferences(
		params.Root, params.DocumentContent, *resolved,
	)
	locations := adminLocalRangesToLocations(params, ranges)
	if params.Context.IncludeDeclaration && resolved.MemberFound {
		members := resolved.Members
		if len(members) == 0 {
			members = []admin.VueComponentSlotMember{resolved.Member}
		}
		for _, member := range members {
			if member.FilePath == "" {
				continue
			}
			if member.NameRange.Declaration || member.NameRange.Identifier {
				locations = append(locations, adminUsageLocation(
					member.FilePath, member.NameRange,
				))
				continue
			}
			line := member.Line
			if line < 1 {
				line = 1
			}
			locations = append(locations, protocol.Location{
				URI: uriutil.FileURI(member.FilePath),
				Range: protocol.Range{
					Start: protocol.Position{Line: line - 1},
					End:   protocol.Position{Line: line - 1},
				},
			})
		}
	}
	return mergeAdminReferenceLocations(locations), true, nil
}

func adminVueLocalReferences(
	params *lsp.ReferenceRequest,
) ([]protocol.Location, bool) {
	if params == nil || params.Root == nil || params.LineIndex == nil ||
		params.ReferenceParams == nil ||
		!adminReferenceTemplate(params) {
		return nil, false
	}
	offset := params.LineIndex.OffsetUTF16(
		uint32(params.Position.Line), uint32(params.Position.Character),
	)
	if access, accessFound := admin.TwigVueExpressionMemberAtOffset(
		params.Root, params.DocumentContent, offset,
	); accessFound && access.Member != "" {
		binding, bindingFound := admin.TwigVueBindingAtOffset(
			params.Root, params.DocumentContent, access.RootRange.Start,
		)
		if bindingFound && binding != nil {
			ranges := admin.TwigVueBindingMemberAccessReferences(
				params.Root, params.DocumentContent, *binding, access,
			)
			return adminLocalRangesToLocations(params, ranges), true
		}
	}
	binding, found := admin.TwigVueBindingAtOffset(
		params.Root, params.DocumentContent, offset,
	)
	if !found || binding == nil {
		return nil, false
	}
	ranges := admin.TwigVueBindingReferences(
		params.Root, params.DocumentContent, *binding,
	)
	if !params.Context.IncludeDeclaration {
		filtered := ranges[:0]
		for _, rangeValue := range ranges {
			if rangeValue != binding.DeclarationRange {
				filtered = append(filtered, rangeValue)
			}
		}
		ranges = filtered
	}
	return adminLocalRangesToLocations(params, ranges), true
}

func (p *AdminReferenceProvider) adminVueInstanceMemberReferences(
	params *lsp.ReferenceRequest,
) ([]protocol.Location, bool, error) {
	if p == nil || p.index == nil || params == nil || params.Root == nil ||
		params.LineIndex == nil || params.ReferenceParams == nil ||
		!adminReferenceTemplate(params) {
		return nil, false, nil
	}
	offset := params.LineIndex.OffsetUTF16(
		uint32(params.Position.Line), uint32(params.Position.Character),
	)
	access, found := admin.TwigVueExpressionMemberAtOffset(
		params.Root, params.DocumentContent, offset,
	)
	if !found || access.Member == "" {
		return nil, false, nil
	}
	templatePath, err := uriutil.Path(params.TextDocument.URI)
	if err != nil {
		return nil, false, err
	}
	liveComponent, err := p.index.GetComponentForDocument(
		templatePath, params.Root, params.SourceString(), params.LineIndex,
	)
	if err != nil {
		return nil, false, err
	}
	target, err := p.index.ResolveTwigVueInstanceMemberForComponent(
		params.Root, params.DocumentContent, offset, templatePath, liveComponent,
	)
	if err != nil || target == nil {
		return nil, target != nil, err
	}
	if !target.MemberFound {
		return nil, true, nil
	}
	var ranges []cst.TextRange
	for _, candidate := range admin.TwigVueExpressionMemberAccesses(
		params.Root, params.DocumentContent,
	) {
		if candidate.Root != access.Root || !candidate.SamePath(access) {
			continue
		}
		resolved, resolveErr := p.index.ResolveTwigVueInstanceMemberForComponent(
			params.Root, params.DocumentContent,
			candidate.MemberRange.Start, templatePath, liveComponent,
		)
		if resolveErr != nil {
			return nil, true, resolveErr
		}
		if resolved == nil || !resolved.MemberFound ||
			resolved.RootMember.Name != target.RootMember.Name ||
			resolved.Member.Name != target.Member.Name ||
			resolved.Member.DefinitionPath != target.Member.DefinitionPath ||
			resolved.Member.DefinitionLine != target.Member.DefinitionLine {
			continue
		}
		ranges = append(ranges, candidate.MemberRange)
	}
	locations := adminLocalRangesToLocations(params, ranges)
	if params.Context.IncludeDeclaration && target.Member.DefinitionPath != "" {
		line := target.Member.DefinitionLine
		if line < 1 {
			line = 1
		}
		locations = append(locations, protocol.Location{
			URI: uriutil.FileURI(target.Member.DefinitionPath),
			Range: protocol.Range{
				Start: protocol.Position{Line: line - 1},
				End:   protocol.Position{Line: line - 1},
			},
		})
	}
	return locations, true, nil
}

func adminLocalRangesToLocations(
	params *lsp.ReferenceRequest,
	ranges []cst.TextRange,
) []protocol.Location {
	locations := make([]protocol.Location, 0, len(ranges))
	for _, rangeValue := range ranges {
		startLine, startCharacter := params.LineIndex.PositionUTF16(
			rangeValue.Start,
		)
		endLine, endCharacter := params.LineIndex.PositionUTF16(rangeValue.End)
		locations = append(locations, protocol.Location{
			URI: params.TextDocument.URI,
			Range: protocol.Range{
				Start: protocol.Position{
					Line: int(startLine), Character: int(startCharacter),
				},
				End: protocol.Position{
					Line: int(endLine), Character: int(endCharacter),
				},
			},
		})
	}
	return locations
}

func (p *AdminReferenceProvider) targetAt(
	params *lsp.ReferenceRequest,
) (admin.AdminSymbolTarget, bool, error) {
	if params == nil || params.Document == nil || params.Root == nil {
		return admin.AdminSymbolTarget{}, false, nil
	}
	ext := strings.ToLower(filepath.Ext(params.TextDocument.URI))
	switch ext {
	case ".js", ".ts":
		path, err := uriutil.Path(params.TextDocument.URI)
		if err != nil {
			return admin.AdminSymbolTarget{}, false, err
		}
		return p.index.JavaScriptSymbolAt(path, params.Node)
	case ".twig":
		if params.LineIndex == nil {
			return admin.AdminSymbolTarget{}, false, nil
		}
		path, err := uriutil.Path(params.TextDocument.URI)
		if err != nil {
			return admin.AdminSymbolTarget{}, false, err
		}
		offset := params.LineIndex.OffsetUTF16(
			uint32(params.Position.Line), uint32(params.Position.Character),
		)
		return p.index.TwigSymbolAt(path, params.Root, offset)
	case ".vue":
		path, err := uriutil.Path(params.TextDocument.URI)
		if err != nil {
			return admin.AdminSymbolTarget{}, false, err
		}
		if adminReferenceScript(params) {
			return p.index.JavaScriptSymbolAt(path, params.Node)
		}
		if !adminReferenceTemplate(params) || params.LineIndex == nil {
			return admin.AdminSymbolTarget{}, false, nil
		}
		offset := params.LineIndex.OffsetUTF16(
			uint32(params.Position.Line), uint32(params.Position.Character),
		)
		return p.index.TwigSymbolAt(path, params.Root, offset)
	default:
		return admin.AdminSymbolTarget{}, false, nil
	}
}

func adminReferenceTemplate(params *lsp.ReferenceRequest) bool {
	if params == nil || params.TextDocument.URI == "" {
		return false
	}
	ext := strings.ToLower(filepath.Ext(params.TextDocument.URI))
	return ext == ".twig" || ext == ".vue" &&
		lsp.EffectiveSyntaxLanguage(language.Vue, params.Node) == language.Twig
}

func adminReferenceScript(params *lsp.ReferenceRequest) bool {
	if params == nil || params.TextDocument.URI == "" {
		return false
	}
	ext := strings.ToLower(filepath.Ext(params.TextDocument.URI))
	return ext == ".js" || ext == ".ts" || ext == ".vue" &&
		lsp.EffectiveSyntaxLanguage(language.Vue, params.Node) == language.JavaScript
}

func (p *AdminReferenceProvider) usageSets(
	target admin.AdminSymbolTarget,
) ([]admin.AdminUsageSet, error) {
	return p.index.GetSymbolUsages(target)
}

func adminUsageLocation(
	filePath string,
	occurrence admin.AdminSourceRange,
) protocol.Location {
	return protocol.Location{
		URI: uriutil.FileURI(filePath),
		Range: protocol.Range{
			Start: protocol.Position{
				Line: occurrence.StartLine, Character: occurrence.StartCharacter,
			},
			End: protocol.Position{
				Line: occurrence.EndLine, Character: occurrence.EndCharacter,
			},
		},
	}
}

func (p *AdminReferenceProvider) declarationLocations(
	target admin.AdminSymbolTarget,
) ([]protocol.Location, error) {
	collector := adminDeclarationCollector{provider: p}
	if err := collector.collect(target); err != nil {
		return nil, err
	}
	return collector.result, nil
}
