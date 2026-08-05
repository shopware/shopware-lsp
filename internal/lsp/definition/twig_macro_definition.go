package definition

import (
	"context"
	"os"
	"path/filepath"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/twig"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

type TwigMacroDefinitionProvider struct {
	index *twig.TwigIndexer
}

func NewTwigMacroDefinitionProvider(
	index *twig.TwigIndexer,
) *TwigMacroDefinitionProvider {
	return &TwigMacroDefinitionProvider{index: index}
}

func (p *TwigMacroDefinitionProvider) GetDefinition(
	_ context.Context,
	request *lsp.DefinitionRequest,
) []protocol.Location {
	if p == nil || p.index == nil || request == nil ||
		request.Node == nil || request.Root == nil ||
		request.LineIndex == nil ||
		filepath.Ext(request.TextDocument.URI) != ".twig" {
		return nil
	}
	offset := request.LineIndex.OffsetUTF16(
		uint32(request.Position.Line),
		uint32(request.Position.Character),
	)
	path, _ := uriutil.Path(request.TextDocument.URI)
	reference, found := twig.MacroReferenceAt(
		path,
		request.Root,
		request.Node,
		offset,
	)
	if !found {
		return nil
	}
	macros := p.definitions(path, request.Root, reference)
	seen := make(map[string]struct{})
	var result []protocol.Location
	for _, macro := range macros {
		var location protocol.Location
		if macro.FilePath == path {
			location = protocol.Location{
				URI: request.TextDocument.URI,
				Range: twigMacroProtocolRange(
					macro.NameRange,
					request.LineIndex,
				),
			}
		} else {
			source, err := os.ReadFile(macro.FilePath)
			if err != nil {
				continue
			}
			location = protocol.Location{
				URI: uriutil.FileURI(macro.FilePath),
				Range: twigMacroProtocolRange(
					macro.NameRange,
					cst.NewLineIndex(string(source)),
				),
			}
		}
		key := location.URI + ":" + macro.NameRange.String()
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, location)
	}
	return result
}

func (p *TwigMacroDefinitionProvider) definitions(
	path string,
	root *cst.Node,
	reference twig.MacroReference,
) []twig.Macro {
	var result []twig.Macro
	for _, template := range reference.Templates {
		macros, err := p.index.FindMacro(template, reference.Name)
		if err == nil {
			result = append(result, macros...)
		}
	}
	currentTemplates := twig.TemplateNames(path)
	for _, target := range reference.Templates {
		if !containsTwigMacroTemplate(currentTemplates, target) {
			continue
		}
		filtered := result[:0]
		for _, macro := range result {
			if macro.FilePath != path {
				filtered = append(filtered, macro)
			}
		}
		result = filtered
		for _, macro := range twig.MacrosInDocument(path, root) {
			if macro.Name == reference.Name {
				result = append(result, macro)
			}
		}
		break
	}
	return result
}

func containsTwigMacroTemplate(values []string, candidate string) bool {
	for _, value := range values {
		if value == candidate {
			return true
		}
	}
	return false
}

func twigMacroProtocolRange(
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
