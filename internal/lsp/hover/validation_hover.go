package hover

import (
	"context"
	"fmt"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/validation"
)

type ValidationHoverProvider struct{}

func NewValidationHoverProvider() *ValidationHoverProvider {
	return &ValidationHoverProvider{}
}

func (p *ValidationHoverProvider) GetHover(
	ctx context.Context,
	request *lsp.HoverRequest,
) (*protocol.Hover, error) {
	if request == nil || request.Node == nil {
		return nil, nil
	}
	reference, found := validation.OptionReferenceAt(
		ctx,
		request.Root,
		request.Node,
	)
	if !found {
		return nil, nil
	}
	property, found := validation.FindConstraintProperty(
		validation.ConstraintPropertiesInContext(
			ctx,
			reference.Constraint,
		),
		reference.Name,
	)
	if !found {
		return nil, nil
	}
	value := fmt.Sprintf(
		"**Symfony constraint option** `%s`\n\nDeclared by `%s`",
		strings.ReplaceAll(property.Name, "`", "\\`"),
		strings.ReplaceAll(property.FullyQualified, "`", "\\`"),
	)
	if !property.Type.IsUnknown() {
		value += "\n\nPHP type: `" + property.Type.String() + "`"
	}
	rng := validationHoverRange(
		reference.Node.RangeTrimmedTrivia(),
		request.LineIndex,
	)
	return &protocol.Hover{
		Contents: protocol.MarkupContent{
			Kind:  protocol.Markdown,
			Value: value,
		},
		Range: &rng,
	}, nil
}

func validationHoverRange(
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
