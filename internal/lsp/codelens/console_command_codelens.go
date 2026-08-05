package codelens

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/console"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

const runSymfonyCommandID = "shopware.symfony.runConsoleCommand"

// ConsoleCommandCodeLensProvider is the portable counterpart of the reference
// plugin's Symfony command run marker. It derives command declarations from
// the current PHP document so unsaved names remain executable.
type ConsoleCommandCodeLensProvider struct {
	root string
}

func NewConsoleCommandCodeLensProvider(
	root string,
) *ConsoleCommandCodeLensProvider {
	return &ConsoleCommandCodeLensProvider{root: root}
}

func (p *ConsoleCommandCodeLensProvider) GetCodeLenses(
	ctx context.Context,
	request *lsp.CodeLensRequest,
) ([]protocol.CodeLens, error) {
	if p == nil || request == nil || request.CodeLensParams == nil ||
		request.Document == nil || request.Document.SyntaxTree == nil ||
		request.Document.SyntaxTree.Root == nil ||
		request.Document.LineIndex == nil {
		return nil, nil
	}
	consolePath := filepath.Join(p.root, "bin", "console")
	if info, err := os.Stat(consolePath); err != nil || info.IsDir() {
		return nil, nil
	}
	path, err := uriutil.Path(request.TextDocument.URI)
	if err != nil {
		return nil, err
	}
	if !strings.EqualFold(filepath.Ext(path), ".php") {
		return nil, nil
	}

	commands := console.ParsePHPCommandsTree(
		path,
		request.Document.SyntaxTree,
	)
	result := make([]protocol.CodeLens, 0, len(commands))
	seen := make(map[string]struct{}, len(commands))
	for _, command := range commands {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if command.Name == "" || command.Name != command.Canonical {
			continue
		}
		key := strings.ToLower(
			command.Class + "\x00" + command.Method + "\x00" + command.Name,
		)
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, protocol.CodeLens{
			Range: relatedProtocolRange(
				command.Range,
				request.Document.LineIndex,
			),
			Command: &protocol.Command{
				Title:   "Run " + command.Name,
				Command: runSymfonyCommandID,
				Arguments: []any{
					command.Name,
					request.TextDocument.URI,
				},
			},
		})
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].Range.Start.Line != result[right].Range.Start.Line {
			return result[left].Range.Start.Line <
				result[right].Range.Start.Line
		}
		if result[left].Range.Start.Character !=
			result[right].Range.Start.Character {
			return result[left].Range.Start.Character <
				result[right].Range.Start.Character
		}
		return result[left].Command.Title < result[right].Command.Title
	})
	return result, nil
}

func (p *ConsoleCommandCodeLensProvider) ResolveCodeLens(
	_ context.Context,
	lens *protocol.CodeLens,
) (*protocol.CodeLens, error) {
	return lens, nil
}

var _ lsp.CodeLensProvider = (*ConsoleCommandCodeLensProvider)(nil)
