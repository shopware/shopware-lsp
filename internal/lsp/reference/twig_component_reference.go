package reference

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/twigcomponent"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

type TwigComponentReferenceProvider struct {
	index    *twigcomponent.Index
	phpIndex *php.PHPIndex
}

func NewTwigComponentReferenceProvider(
	index *twigcomponent.Index,
	phpIndex *php.PHPIndex,
) *TwigComponentReferenceProvider {
	return &TwigComponentReferenceProvider{
		index:    index,
		phpIndex: phpIndex,
	}
}

func (p *TwigComponentReferenceProvider) GetReferences(
	ctx context.Context,
	request *lsp.ReferenceRequest,
) ([]protocol.Location, error) {
	if p == nil || p.index == nil || request == nil ||
		request.Root == nil || request.LineIndex == nil {
		return nil, nil
	}
	offset := request.LineIndex.OffsetUTF16(
		uint32(request.Position.Line),
		uint32(request.Position.Character),
	)
	currentPath, _ := uriutil.Path(request.TextDocument.URI)
	name := ""
	switch strings.ToLower(filepath.Ext(currentPath)) {
	case ".twig":
		if action, found := twigcomponent.LiveActionReferenceAt(
			currentPath,
			request.Root,
			offset,
		); found && action.Name != "" {
			return p.liveActionReferences(
				currentPath,
				action.Name,
				request,
				nil,
			)
		}
		usage, found := twigcomponent.UsageAt(
			currentPath,
			request.Root,
			offset,
		)
		if !found {
			return nil, nil
		}
		name = usage.Name
	case ".php":
		if actions, found := p.liveActionsAtPHP(
			ctx,
			offset,
		); found {
			return p.liveActionReferences(
				currentPath,
				actions[0].Name,
				request,
				actions,
			)
		}
		declaration, found := twigcomponent.DeclarationAtPHP(
			currentPath,
			request.Root,
			offset,
		)
		if !found {
			return nil, nil
		}
		component, resolved, err := p.index.ResolveDeclaration(declaration)
		if err != nil || !resolved {
			return nil, err
		}
		name = component.Name
	default:
		return nil, nil
	}

	var result []protocol.Location
	usages, err := p.index.Usages(name)
	if err != nil {
		return nil, err
	}
	for _, usage := range usages {
		if usage.File == currentPath {
			continue
		}
		if location, ok := componentReferenceLocation(
			usage.File,
			usage.Range,
		); ok {
			result = append(result, location)
		}
	}
	if strings.EqualFold(filepath.Ext(currentPath), ".twig") {
		for _, usage := range twigcomponent.UsagesInTwig(
			currentPath,
			request.Root,
		) {
			if usage.Name != name {
				continue
			}
			result = append(result, protocol.Location{
				URI: request.TextDocument.URI,
				Range: twigComponentReferenceRange(
					usage.Range,
					request.LineIndex,
				),
			})
		}
	}
	if request.Context.IncludeDeclaration {
		declarations, declarationErr := p.declarationLocations(name)
		if declarationErr != nil {
			return nil, declarationErr
		}
		result = append(result, declarations...)
	}
	return uniqueComponentReferenceLocations(result), nil
}

func (p *TwigComponentReferenceProvider) liveActionsAtPHP(
	ctx context.Context,
	offset uint32,
) ([]twigcomponent.LiveAction, bool) {
	phpContext := php.GetPHPContext(ctx)
	if phpContext == nil || phpContext.Document == nil ||
		phpContext.Snapshot == nil {
		return nil, false
	}
	symbol, found := php.SymbolAt(
		phpContext.Document,
		phpContext.Snapshot,
		offset,
	)
	if !found || !symbol.IsFunctionLike() {
		return nil, false
	}
	components, err := p.index.Components()
	if err != nil {
		return nil, false
	}
	var result []twigcomponent.LiveAction
	for _, component := range components {
		actions, actionErr := p.index.LiveActions(component.Name)
		if actionErr != nil {
			continue
		}
		for _, action := range actions {
			if action.File == symbol.Path &&
				action.Range == symbol.SelectionRange {
				result = append(result, action)
			}
		}
	}
	return result, len(result) != 0
}

func (p *TwigComponentReferenceProvider) liveActionReferences(
	currentPath,
	actionName string,
	request *lsp.ReferenceRequest,
	knownActions []twigcomponent.LiveAction,
) ([]protocol.Location, error) {
	components := make(map[string]twigcomponent.Component)
	var actions []twigcomponent.LiveAction
	if strings.EqualFold(filepath.Ext(currentPath), ".twig") {
		current, err := p.index.ComponentsForTemplate(currentPath)
		if err != nil {
			return nil, err
		}
		for _, component := range current {
			components[strings.ToLower(component.Name)] = component
		}
		currentActions, err := p.index.LiveActionsForTemplate(currentPath)
		if err != nil {
			return nil, err
		}
		for _, action := range currentActions {
			if strings.EqualFold(action.Name, actionName) {
				actions = append(actions, action)
			}
		}
	} else {
		all, err := p.index.Components()
		if err != nil {
			return nil, err
		}
		for _, action := range knownActions {
			if !strings.EqualFold(action.Name, actionName) {
				continue
			}
			actions = append(actions, action)
			for _, component := range all {
				componentActions, actionErr := p.index.LiveActions(
					component.Name,
				)
				if actionErr != nil {
					continue
				}
				for _, candidate := range componentActions {
					if candidate.File == action.File &&
						candidate.Range == action.Range {
						components[strings.ToLower(component.Name)] =
							component
						break
					}
				}
			}
		}
	}
	if len(components) == 0 || len(actions) == 0 {
		return nil, nil
	}

	var result []protocol.Location
	for _, component := range components {
		references, err := p.index.LiveActionReferences(
			component.Name,
			actionName,
		)
		if err != nil {
			return nil, err
		}
		for _, reference := range references {
			if reference.File == currentPath {
				continue
			}
			if location, ok := componentReferenceLocation(
				reference.File,
				reference.Range,
			); ok {
				result = append(result, location)
			}
		}
	}
	if strings.EqualFold(filepath.Ext(currentPath), ".twig") {
		for _, reference := range twigcomponent.LiveActionReferencesInTwig(
			currentPath,
			request.Root,
		) {
			if !strings.EqualFold(reference.Name, actionName) {
				continue
			}
			result = append(result, protocol.Location{
				URI: request.TextDocument.URI,
				Range: twigComponentReferenceRange(
					reference.Range,
					request.LineIndex,
				),
			})
		}
	}
	if request.Context.IncludeDeclaration {
		for _, action := range actions {
			if location, ok := componentReferenceLocation(
				action.File,
				action.Range,
			); ok {
				result = append(result, location)
			}
		}
	}
	return uniqueComponentReferenceLocations(result), nil
}

func (p *TwigComponentReferenceProvider) declarationLocations(
	name string,
) ([]protocol.Location, error) {
	components, err := p.index.Find(name)
	if err != nil {
		return nil, err
	}
	var result []protocol.Location
	for _, component := range components {
		if component.Class != "" && p.phpIndex != nil {
			if class, found := p.phpIndex.FindClass(component.Class); found {
				if location, ok := componentReferenceLocation(
					class.Path,
					class.SelectionRange,
				); ok {
					result = append(result, location)
					continue
				}
			}
		}
		templates, templateErr := p.index.TemplateFiles(component)
		if templateErr != nil {
			return nil, templateErr
		}
		for _, template := range templates {
			if location, ok := componentReferenceLocation(
				template,
				cst.TextRange{},
			); ok {
				result = append(result, location)
			}
		}
	}
	return result, nil
}

func componentReferenceLocation(
	path string,
	rng cst.TextRange,
) (protocol.Location, bool) {
	source, err := os.ReadFile(path)
	if err != nil {
		return protocol.Location{}, false
	}
	return protocol.Location{
		URI: uriutil.FileURI(path),
		Range: twigComponentReferenceRange(
			rng,
			cst.NewLineIndex(string(source)),
		),
	}, true
}

func twigComponentReferenceRange(
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

func uniqueComponentReferenceLocations(
	locations []protocol.Location,
) []protocol.Location {
	seen := make(map[string]struct{}, len(locations))
	result := make([]protocol.Location, 0, len(locations))
	for _, location := range locations {
		key := fmt.Sprintf(
			"%s:%d:%d:%d:%d",
			location.URI,
			location.Range.Start.Line,
			location.Range.Start.Character,
			location.Range.End.Line,
			location.Range.End.Character,
		)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, location)
	}
	return result
}
