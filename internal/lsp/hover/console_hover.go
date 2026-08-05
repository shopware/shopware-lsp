package hover

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/console"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
)

type ConsoleHoverProvider struct {
	root  string
	index *console.Index
}

func NewConsoleHoverProvider(
	root string,
	index *console.Index,
) *ConsoleHoverProvider {
	return &ConsoleHoverProvider{root: root, index: index}
}

func (p *ConsoleHoverProvider) GetHover(
	ctx context.Context,
	request *lsp.HoverRequest,
) (*protocol.Hover, error) {
	if p == nil || p.index == nil || request == nil ||
		request.Node == nil ||
		strings.ToLower(filepath.Ext(request.TextDocument.URI)) != ".php" {
		return nil, nil
	}
	reference, ok := console.ReferenceAt(request.Node)
	if !ok || !console.ValidateReference(ctx, reference) {
		return nil, nil
	}
	var markdown strings.Builder
	if reference.Role == console.ReferenceCommand {
		commands, err := p.index.GetCommand(reference.Name)
		if err != nil || len(commands) == 0 {
			return nil, err
		}
		command := commands[0]
		fmt.Fprintf(
			&markdown,
			"**Symfony command** `%s`\n\n",
			command.Name,
		)
		if command.Description != "" {
			fmt.Fprintf(&markdown, "%s\n\n", command.Description)
		}
		target := command.Class
		if command.Method != "" {
			target += "::" + command.Method + "()"
		}
		fmt.Fprintf(&markdown, "Handler: `%s`", target)
		if len(command.Arguments) != 0 || len(command.Options) != 0 {
			fmt.Fprintf(
				&markdown,
				"\n\n%d arguments · %d options",
				len(command.Arguments),
				len(command.Options),
			)
		}
	} else {
		inputs, err := console.InputsForReference(
			ctx,
			p.index,
			reference,
		)
		if err != nil {
			return nil, err
		}
		var input *console.Input
		for index := range inputs {
			if inputs[index].Name == reference.Name ||
				inputs[index].Kind == console.Option &&
					inputs[index].Shortcut == reference.Name {
				input = &inputs[index]
				break
			}
		}
		if input == nil {
			return nil, nil
		}
		kind := "argument"
		if input.Kind == console.Option {
			kind = "option"
		}
		fmt.Fprintf(
			&markdown,
			"**Symfony command %s** `%s`\n",
			kind,
			input.Name,
		)
		if input.Shortcut != "" {
			fmt.Fprintf(&markdown, "\nShortcut: `-%s`\n", input.Shortcut)
		}
		if input.Description != "" {
			fmt.Fprintf(&markdown, "\n%s\n", input.Description)
		}
		if input.Mode != "" {
			fmt.Fprintf(&markdown, "\nMode: `%s`\n", input.Mode)
		}
		if input.Default != "" {
			fmt.Fprintf(&markdown, "\nDefault: `%s`\n", input.Default)
		}
		displayPath, pathErr := filepath.Rel(p.root, input.File)
		if pathErr != nil {
			displayPath = input.File
		}
		fmt.Fprintf(
			&markdown,
			"\nDefined in `%s`",
			filepath.ToSlash(displayPath),
		)
	}

	rng := reference.Node.RangeTrimmedTrivia()
	startLine, startCharacter := request.LineIndex.PositionUTF16(rng.Start)
	endLine, endCharacter := request.LineIndex.PositionUTF16(rng.End)
	return &protocol.Hover{
		Contents: protocol.MarkupContent{
			Kind:  protocol.Markdown,
			Value: markdown.String(),
		},
		Range: &protocol.Range{
			Start: protocol.Position{
				Line:      int(startLine),
				Character: int(startCharacter),
			},
			End: protocol.Position{
				Line:      int(endLine),
				Character: int(endCharacter),
			},
		},
	}, nil
}
