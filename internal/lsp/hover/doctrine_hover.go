package hover

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/doctrine"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

type DoctrineHoverProvider struct {
	index    *doctrine.Index
	phpIndex *php.PHPIndex
}

func NewDoctrineHoverProvider(
	index *doctrine.Index,
	phpIndexes ...*php.PHPIndex,
) *DoctrineHoverProvider {
	var phpIndex *php.PHPIndex
	if len(phpIndexes) != 0 {
		phpIndex = phpIndexes[0]
	}
	return &DoctrineHoverProvider{
		index:    index,
		phpIndex: phpIndex,
	}
}

func (p *DoctrineHoverProvider) GetHover(
	ctx context.Context,
	request *lsp.HoverRequest,
) (*protocol.Hover, error) {
	if p == nil || p.index == nil || request == nil ||
		request.Root == nil || request.Document == nil {
		return nil, nil
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
	); found && registration.Name != "" && registration.Class != "" {
		markdown := "**Doctrine DBAL type registration** `" +
			escapeDoctrineMarkdown(registration.Name) + "`\n\nClass: `" +
			escapeDoctrineMarkdown(registration.Class) + "`"
		if p.phpIndex != nil {
			if symbol, classFound := p.phpIndex.FindClass(
				registration.Class,
			); classFound && symbol.DocSummary() != "" {
				markdown += "\n\n" +
					escapeDoctrineMarkdown(symbol.DocSummary())
			}
		}
		return doctrineHover(
			markdown,
			registration.Range,
			request.LineIndex,
		), nil
	}
	if extension != ".php" {
		return nil, nil
	}
	if request.Node == nil {
		return nil, nil
	}
	if reference, found := p.index.DBALReferenceAt(
		ctx,
		request.Root,
		request.Node,
	); found {
		switch reference.Role {
		case doctrine.DBALTableReference:
			model, exists, err := p.index.DefinitionForTable(reference.Name)
			if err != nil || !exists {
				return nil, err
			}
			return doctrineHover("**Shopware DBAL table** `"+escapeDoctrineMarkdown(model.Name)+"`\n\nDefinition: `"+escapeDoctrineMarkdown(model.Class)+"`", reference.Range, request.LineIndex), nil
		case doctrine.DBALColumnReference:
			model, field, exists, err := p.index.FieldForColumn(
				reference.Table,
				reference.Name,
			)
			if err != nil || !exists {
				return nil, err
			}
			return doctrineHover("**Shopware DBAL column** `"+escapeDoctrineMarkdown(reference.Name)+"`\n\nField: `"+escapeDoctrineMarkdown(model.Class+"::"+field.Name)+"`\n\nType: `"+escapeDoctrineMarkdown(field.Type)+"`", reference.Range, request.LineIndex), nil
		case doctrine.DBALAliasReference:
			return doctrineHover(
				"**Doctrine DBAL join alias** `"+
					escapeDoctrineMarkdown(reference.Name)+"`",
				reference.Range,
				request.LineIndex,
			), nil
		}
	}
	return nil, nil
}

func doctrineHover(
	markdown string,
	rng cst.TextRange,
	lineIndex *cst.LineIndex,
) *protocol.Hover {
	startLine, startCharacter := lineIndex.PositionUTF16(rng.Start)
	endLine, endCharacter := lineIndex.PositionUTF16(rng.End)
	return &protocol.Hover{
		Contents: protocol.MarkupContent{
			Kind:  protocol.Markdown,
			Value: markdown,
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
	}
}

func escapeDoctrineMarkdown(value string) string {
	return strings.ReplaceAll(value, "`", "\\`")
}
