package codelens

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/serializer"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

type SerializerCodeLensProvider struct {
	index    *serializer.Index
	phpIndex *php.PHPIndex
}

func NewSerializerCodeLensProvider(
	index *serializer.Index,
	phpIndex *php.PHPIndex,
) *SerializerCodeLensProvider {
	return &SerializerCodeLensProvider{
		index:    index,
		phpIndex: phpIndex,
	}
}

func (p *SerializerCodeLensProvider) GetCodeLenses(
	_ context.Context,
	request *lsp.CodeLensRequest,
) ([]protocol.CodeLens, error) {
	if p == nil || p.index == nil || p.phpIndex == nil ||
		request == nil || request.CodeLensParams == nil ||
		request.Document == nil ||
		!strings.HasSuffix(
			strings.ToLower(request.TextDocument.URI),
			".php",
		) {
		return nil, nil
	}
	path, err := uriutil.Path(request.TextDocument.URI)
	if err != nil {
		return nil, err
	}
	var result []protocol.CodeLens
	for _, class := range p.phpIndex.ClassSymbolsIn(path) {
		usages, usageErr := p.index.Usages(class.FullyQualified)
		if usageErr != nil {
			return nil, usageErr
		}
		if len(usages) == 0 {
			continue
		}
		targets := make([]string, 0, len(usages))
		for _, usage := range usages {
			line := serializerUsageLine(usage)
			targets = append(
				targets,
				uriutil.FileURIWithFragment(
					usage.File,
					strconv.Itoa(line),
				),
			)
		}
		startLine, startCharacter := request.Document.LineIndex.
			PositionUTF16(class.SelectionRange.Start)
		endLine, endCharacter := request.Document.LineIndex.
			PositionUTF16(class.SelectionRange.End)
		result = append(result, protocol.CodeLens{
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
			Command: &protocol.Command{
				Title: fmt.Sprintf(
					"%d serializer use(s)",
					len(usages),
				),
				Command:   "shopware.openReferences",
				Arguments: []any{targets},
			},
		})
	}
	return result, nil
}

func (p *SerializerCodeLensProvider) ResolveCodeLens(
	_ context.Context,
	lens *protocol.CodeLens,
) (*protocol.CodeLens, error) {
	return lens, nil
}

func serializerUsageLine(usage serializer.Usage) int {
	source, err := os.ReadFile(usage.File)
	if err != nil {
		return 0
	}
	line, _ := cst.NewLineIndex(string(source)).
		PositionUTF16(usage.Range.Start)
	return int(line)
}
