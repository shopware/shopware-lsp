package definition

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/twigcomponent"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

type TwigComponentDefinitionProvider struct {
	index    *twigcomponent.Index
	phpIndex *php.PHPIndex
}

func NewTwigComponentDefinitionProvider(
	index *twigcomponent.Index,
	phpIndex *php.PHPIndex,
) *TwigComponentDefinitionProvider {
	return &TwigComponentDefinitionProvider{
		index:    index,
		phpIndex: phpIndex,
	}
}

func (p *TwigComponentDefinitionProvider) GetDefinition(
	_ context.Context,
	request *lsp.DefinitionRequest,
) []protocol.Location {
	if p == nil || p.index == nil || request == nil ||
		request.Root == nil || request.LineIndex == nil ||
		!strings.HasSuffix(
			strings.ToLower(request.TextDocument.URI),
			".twig",
		) {
		return nil
	}
	offset := request.LineIndex.OffsetUTF16(
		uint32(request.Position.Line),
		uint32(request.Position.Character),
	)
	path, _ := uriutil.Path(request.TextDocument.URI)
	if argument, found := twigcomponent.LiveActionArgumentReferenceAt(
		path,
		request.Root,
		offset,
	); found {
		return p.liveActionArgumentDefinitions(path, argument)
	}
	if action, found := twigcomponent.LiveActionReferenceAt(
		path,
		request.Root,
		offset,
	); found && action.Name != "" {
		return p.liveActionDefinitions(path, action.Name)
	}
	if root, member, _, found := twigcomponent.AccessorMemberAt(
		request.Node,
	); found && root == "computed" {
		if locations := p.computedDefinitions(path, member, request); len(locations) != 0 {
			return locations
		}
	}
	if name, _, variable := twigcomponent.VariableAt(
		request.Node,
	); variable {
		if locations := p.variableDefinitions(
			path,
			name,
			request,
		); len(locations) != 0 {
			return locations
		}
	}
	if block, found := twigcomponent.BlockUsageAt(
		request.Node,
		offset,
	); found {
		return p.blockDefinitions(block)
	}
	usage, found := twigcomponent.UsageAt(
		path,
		request.Root,
		offset,
	)
	if !found {
		prop, propFound := twigcomponent.PropUsageAt(
			request.Root,
			request.Node,
			offset,
		)
		if !propFound {
			return nil
		}
		return p.propDefinitions(prop)
	}
	components, err := p.index.Find(usage.Name)
	if err != nil {
		return nil
	}
	var result []protocol.Location
	for _, component := range components {
		if location, ok := p.classLocation(component); ok {
			result = append(result, location)
			continue
		}
		templates, templateErr := p.index.TemplateFiles(component)
		if templateErr != nil {
			continue
		}
		for _, template := range templates {
			if location, ok := componentFileLocation(
				template,
				cst.TextRange{},
			); ok {
				result = append(result, location)
			}
		}
	}
	return uniqueComponentLocations(result)
}

func (p *TwigComponentDefinitionProvider) liveActionArgumentDefinitions(
	path string,
	reference twigcomponent.LiveActionArgumentReference,
) []protocol.Location {
	actions, err := p.index.LiveActionsForTemplate(path)
	if err != nil {
		return nil
	}
	var result []protocol.Location
	for _, action := range actions {
		if !strings.EqualFold(action.Name, reference.Action) {
			continue
		}
		for _, parameter := range action.Parameters {
			if !strings.EqualFold(parameter.Name, reference.Name) {
				continue
			}
			if location, ok := componentFileLocation(
				action.File,
				parameter.Range,
			); ok {
				result = append(result, location)
			}
		}
	}
	return uniqueComponentLocations(result)
}

func (p *TwigComponentDefinitionProvider) liveActionDefinitions(
	path,
	name string,
) []protocol.Location {
	actions, err := p.index.LiveActionsForTemplate(path)
	if err != nil {
		return nil
	}
	var result []protocol.Location
	for _, action := range actions {
		if !strings.EqualFold(action.Name, name) {
			continue
		}
		if location, ok := componentFileLocation(
			action.File,
			action.Range,
		); ok {
			result = append(result, location)
		}
	}
	return uniqueComponentLocations(result)
}

func (p *TwigComponentDefinitionProvider) blockDefinitions(
	usage twigcomponent.ComponentBlockUsage,
) []protocol.Location {
	blocks, err := p.index.Blocks(usage.Component)
	if err != nil {
		return nil
	}
	var result []protocol.Location
	for _, block := range blocks {
		if block.Name != usage.Name || block.File == "" {
			continue
		}
		line := block.Line - 1
		if line < 0 {
			line = 0
		}
		result = append(result, protocol.Location{
			URI: uriutil.FileURI(block.File),
			Range: protocol.Range{
				Start: protocol.Position{Line: line},
				End:   protocol.Position{Line: line},
			},
		})
	}
	return uniqueComponentLocations(result)
}

func (p *TwigComponentDefinitionProvider) variableDefinitions(
	path,
	name string,
	request *lsp.DefinitionRequest,
) []protocol.Location {
	components, props, err := p.index.ContextForTemplate(
		path,
		request.Root,
	)
	if err != nil || len(components) == 0 {
		return nil
	}
	var result []protocol.Location
	if name == "this" || name == "computed" {
		for _, component := range components {
			if location, ok := p.classLocation(component); ok {
				result = append(result, location)
			}
		}
		return uniqueComponentLocations(result)
	}
	for _, prop := range props {
		if !strings.EqualFold(prop.Name, name) {
			continue
		}
		if prop.File == path {
			result = append(result, protocol.Location{
				URI: request.TextDocument.URI,
				Range: twigComponentProtocolRange(
					prop.Range,
					request.LineIndex,
				),
			})
			continue
		}
		if location, ok := componentFileLocation(
			prop.File,
			prop.Range,
		); ok {
			result = append(result, location)
		}
	}
	return uniqueComponentLocations(result)
}

func (p *TwigComponentDefinitionProvider) computedDefinitions(
	path,
	name string,
	request *lsp.DefinitionRequest,
) []protocol.Location {
	computed, err := p.index.ComputedForTemplate(path)
	if err != nil {
		return nil
	}
	var result []protocol.Location
	for _, prop := range computed {
		if !strings.EqualFold(prop.Name, name) {
			continue
		}
		if prop.File == path {
			result = append(result, protocol.Location{
				URI: request.TextDocument.URI,
				Range: twigComponentProtocolRange(
					prop.Range,
					request.LineIndex,
				),
			})
			continue
		}
		if location, ok := componentFileLocation(
			prop.File,
			prop.Range,
		); ok {
			result = append(result, location)
		}
	}
	return uniqueComponentLocations(result)
}

func (p *TwigComponentDefinitionProvider) propDefinitions(
	usage twigcomponent.PropUsage,
) []protocol.Location {
	props, err := p.index.Props(usage.Component)
	if err != nil {
		return nil
	}
	var result []protocol.Location
	for _, prop := range props {
		if !strings.EqualFold(prop.Name, usage.Name) {
			continue
		}
		if location, ok := componentFileLocation(
			prop.File,
			prop.Range,
		); ok {
			result = append(result, location)
		}
	}
	return uniqueComponentLocations(result)
}

func (p *TwigComponentDefinitionProvider) classLocation(
	component twigcomponent.Component,
) (protocol.Location, bool) {
	if component.Class != "" && p.phpIndex != nil {
		if class, found := p.phpIndex.FindClass(component.Class); found {
			return componentFileLocation(
				class.Path,
				class.SelectionRange,
			)
		}
	}
	if component.File == "" {
		return protocol.Location{}, false
	}
	rng := component.ClassRange
	if rng.Len() == 0 {
		rng = component.NameRange
	}
	return componentFileLocation(component.File, rng)
}

func componentFileLocation(
	path string,
	rng cst.TextRange,
) (protocol.Location, bool) {
	source, err := os.ReadFile(path)
	if err != nil {
		return protocol.Location{}, false
	}
	lineIndex := cst.NewLineIndex(string(source))
	return protocol.Location{
		URI:   uriutil.FileURI(path),
		Range: twigComponentProtocolRange(rng, lineIndex),
	}, true
}

func twigComponentProtocolRange(
	rng cst.TextRange,
	lineIndex *cst.LineIndex,
) protocol.Range {
	if lineIndex == nil {
		return protocol.Range{}
	}
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

func uniqueComponentLocations(
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
