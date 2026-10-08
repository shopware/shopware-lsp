package definition

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/doctrine"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

type DoctrineDefinitionProvider struct {
	index    *doctrine.Index
	phpIndex *php.PHPIndex
}

func NewDoctrineDefinitionProvider(
	index *doctrine.Index,
	phpIndex *php.PHPIndex,
) *DoctrineDefinitionProvider {
	return &DoctrineDefinitionProvider{
		index:    index,
		phpIndex: phpIndex,
	}
}

func (p *DoctrineDefinitionProvider) GetDefinition(
	ctx context.Context,
	request *lsp.DefinitionRequest,
) []protocol.Location {
	if p == nil || p.index == nil || request == nil ||
		request.Root == nil || request.Document == nil {
		return nil
	}
	path, _ := uriutil.Path(request.TextDocument.URI)
	extension := strings.ToLower(filepath.Ext(path))
	offset := request.LineIndex.OffsetUTF16(
		uint32(request.Position.Line),
		uint32(request.Position.Character),
	)
	if registration, found := doctrine.TypeRegistrationReferenceAt(
		path,
		request.Root,
		offset,
	); found && registration.Class != "" && p.phpIndex != nil {
		if symbol, classFound := p.phpIndex.FindClass(
			registration.Class,
		); classFound {
			return []protocol.Location{phpSymbolLocation(symbol)}
		}
	}
	if extension != ".php" {
		return nil
	}
	if request.Node == nil {
		return nil
	}
	if reference, found := p.index.DBALReferenceAt(
		ctx,
		request.Root,
		request.Node,
	); found {
		switch reference.Role {
		case doctrine.DBALTableReference:
			model, exists, err := p.index.DefinitionForTable(reference.Name)
			if err == nil && exists {
				return p.dalLocation(model.File, model.ClassRange)
			}
		case doctrine.DBALColumnReference:
			model, field, exists, err := p.index.FieldForColumn(
				reference.Table,
				reference.Name,
			)
			if err == nil && exists {
				return p.dalLocation(model.File, field.Range)
			}
		}
	}
	return nil
}

func (p *DoctrineDefinitionProvider) dalLocation(path string, rng cst.TextRange) []protocol.Location {
	source, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	lines := cst.NewLineIndex(string(source))
	sl, sc := lines.PositionUTF16(rng.Start)
	el, ec := lines.PositionUTF16(rng.End)
	return []protocol.Location{{URI: uriutil.FileURI(path), Range: protocol.Range{Start: protocol.Position{Line: int(sl), Character: int(sc)}, End: protocol.Position{Line: int(el), Character: int(ec)}}}}
}
