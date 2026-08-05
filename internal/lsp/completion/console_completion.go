package completion

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/console"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
)

type ConsoleCompletionProvider struct {
	index *console.Index
}

func NewConsoleCompletionProvider(
	index *console.Index,
) *ConsoleCompletionProvider {
	return &ConsoleCompletionProvider{index: index}
}

func (p *ConsoleCompletionProvider) GetCompletions(
	ctx context.Context,
	request *lsp.CompletionRequest,
) []protocol.CompletionItem {
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
		commands, err := p.index.GetCommands()
		if err != nil {
			return nil
		}
		seen := make(map[string]struct{}, len(commands))
		items := make([]protocol.CompletionItem, 0, len(commands))
		for _, command := range commands {
			if _, exists := seen[command.Name]; exists {
				continue
			}
			seen[command.Name] = struct{}{}
			detail := command.Description
			if detail == "" {
				detail = command.Class
			}
			items = append(items, protocol.CompletionItem{
				Label:  command.Name,
				Kind:   int(protocol.ReferenceCompletion),
				Detail: detail,
			})
		}
		return items
	}

	inputs, err := console.InputsForReference(ctx, p.index, reference)
	if err != nil {
		return nil
	}
	items := make([]protocol.CompletionItem, 0, len(inputs))
	for _, input := range inputs {
		detail := input.Description
		if detail == "" {
			detail = input.Mode
		}
		if input.Default != "" {
			if detail != "" {
				detail += " · "
			}
			detail += "default " + input.Default
		}
		items = append(items, protocol.CompletionItem{
			Label:  input.Name,
			Kind:   int(protocol.FieldCompletion),
			Detail: detail,
		})
	}
	return items
}

func (p *ConsoleCompletionProvider) GetTriggerCharacters() []string {
	return []string{"'", "\""}
}
