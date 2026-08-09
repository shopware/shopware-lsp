package phpsemantic

import (
	"context"
	"fmt"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
)

func (p *Provider) Rename(
	ctx context.Context,
	request *lsp.RenameRequest,
) (*protocol.WorkspaceEdit, error) {
	if !isPHP(request.TextDocument.URI) || request.LineIndex == nil {
		return nil, nil
	}
	phpContext := php.GetPHPContext(ctx)
	if phpContext == nil || phpContext.Document == nil || phpContext.Snapshot == nil {
		return nil, nil
	}
	offset := request.LineIndex.OffsetUTF16(
		uint32(request.Position.Line),
		uint32(request.Position.Character),
	)
	symbol, ok := php.SymbolAt(phpContext.Document, phpContext.Snapshot, offset)
	if !ok {
		return nil, nil
	}
	if symbol.Flags.Has(semantic.InternalFlag) || strings.HasPrefix(symbol.Path, "phpstub://") {
		return nil, fmt.Errorf("cannot rename internal PHP symbol %s", symbol.Name)
	}
	newName := strings.TrimPrefix(strings.TrimSpace(request.NewName), "$")
	if !validPHPIdentifier(newName) {
		return nil, fmt.Errorf("%q is not a valid PHP identifier", request.NewName)
	}

	cache := newLocationCache(request.Document)
	edit := &protocol.WorkspaceEdit{Changes: make(map[string][]protocol.TextEdit)}
	seen := make(map[string]struct{})
	add := func(path string, textRange cst.TextRange) {
		key := fmt.Sprintf("%s:%d:%d", path, textRange.Start, textRange.End)
		if _, exists := seen[key]; exists {
			return
		}
		seen[key] = struct{}{}
		location := cache.textRange(path, textRange)
		replacement := cache.renameText(path, textRange, newName)
		edit.Changes[location.URI] = append(edit.Changes[location.URI], protocol.TextEdit{
			Range:   location.Range,
			NewText: replacement,
		})
	}
	add(symbol.Path, symbol.SelectionRange)
	for _, reference := range phpContext.Snapshot.ReferencesTo(symbol.ID) {
		add(
			reference.Path,
			cst.TextRange{Start: reference.RangeStart, End: reference.RangeEnd},
		)
	}
	return edit, nil
}
