package definition

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/console"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

type ConsoleDefinitionProvider struct {
	index *console.Index
}

func NewConsoleDefinitionProvider(
	index *console.Index,
) *ConsoleDefinitionProvider {
	return &ConsoleDefinitionProvider{index: index}
}

func (p *ConsoleDefinitionProvider) GetDefinition(
	ctx context.Context,
	request *lsp.DefinitionRequest,
) []protocol.Location {
	if p == nil || p.index == nil || request == nil ||
		request.Node == nil ||
		strings.ToLower(filepath.Ext(request.TextDocument.URI)) != ".php" {
		return nil
	}
	reference, ok := console.ReferenceAt(request.Node)
	if !ok || !console.ValidateReference(ctx, reference) {
		return nil
	}
	if reference.Role == console.ReferenceCommand {
		commands, err := p.index.GetCommand(reference.Name)
		if err != nil {
			return nil
		}
		locations := make([]protocol.Location, 0, len(commands))
		for _, command := range commands {
			if location, found := consoleLocation(
				command.File,
				command.Range,
			); found {
				locations = append(locations, location)
			}
		}
		return locations
	}
	inputs, err := console.InputsForReference(ctx, p.index, reference)
	if err != nil {
		return nil
	}
	var locations []protocol.Location
	for _, input := range inputs {
		if input.Name != reference.Name &&
			(input.Kind != console.Option ||
				input.Shortcut != reference.Name) {
			continue
		}
		if location, found := consoleLocation(input.File, input.Range); found {
			locations = append(locations, location)
		}
	}
	return locations
}

func consoleLocation(
	path string,
	rng cst.TextRange,
) (protocol.Location, bool) {
	source, err := os.ReadFile(path)
	if err != nil {
		return protocol.Location{}, false
	}
	lineIndex := cst.NewLineIndex(string(source))
	startLine, startCharacter := lineIndex.PositionUTF16(rng.Start)
	endLine, endCharacter := lineIndex.PositionUTF16(rng.End)
	return protocol.Location{
		URI: uriutil.FileURI(path),
		Range: protocol.Range{
			Start: protocol.Position{
				Line:      int(startLine),
				Character: int(startCharacter),
			},
			End: protocol.Position{
				Line:      int(endLine),
				Character: int(endCharacter),
			},
		},
	}, true
}
