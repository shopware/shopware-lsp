package hover

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/translation"
)

type TranslationHoverProvider struct {
	root     string
	index    *translation.Index
	phpIndex *php.PHPIndex
}

func NewTranslationHoverProvider(
	root string,
	index *translation.Index,
	phpIndex *php.PHPIndex,
) *TranslationHoverProvider {
	return &TranslationHoverProvider{
		root:     root,
		index:    index,
		phpIndex: phpIndex,
	}
}

func (p *TranslationHoverProvider) GetHover(
	ctx context.Context,
	request *lsp.HoverRequest,
) (*protocol.Hover, error) {
	if p == nil || p.index == nil || request == nil || request.Node == nil {
		return nil, nil
	}
	extension := strings.ToLower(filepath.Ext(request.TextDocument.URI))
	if extension != ".php" && extension != ".twig" {
		return nil, nil
	}
	reference, ok := translation.ReferenceAt(
		request.TextDocument.URI,
		request.Node,
		request.DocumentContent,
	)
	if !ok || extension == ".php" &&
		!translation.ValidatePHPReference(
			ctx,
			reference,
			p.phpIndex,
			request.DocumentContent,
		) {
		return nil, nil
	}

	var markdown strings.Builder
	switch reference.Role {
	case translation.ReferenceKey:
		messages, err := p.index.GetMessages(reference.Domain, reference.Key)
		if err != nil || len(messages) == 0 {
			return nil, err
		}
		fmt.Fprintf(
			&markdown,
			"**Symfony translation** `%s` · `%s`\n\n",
			escapeTranslationMarkdown(reference.Key),
			escapeTranslationMarkdown(reference.Domain),
		)
		for _, message := range messages {
			displayPath, pathErr := filepath.Rel(p.root, message.File)
			if pathErr != nil {
				displayPath = message.File
			}
			fmt.Fprintf(
				&markdown,
				"- **%s**: %s  \n  `%s:%d`\n",
				escapeTranslationMarkdown(message.Locale),
				escapeTranslationMarkdown(message.Text),
				escapeTranslationMarkdown(filepath.ToSlash(displayPath)),
				message.Line+1,
			)
		}
	case translation.ReferenceDomain:
		keys, err := p.index.GetKeys(reference.Domain)
		if err != nil || len(keys) == 0 {
			return nil, err
		}
		fmt.Fprintf(
			&markdown,
			"**Symfony translation domain** `%s`\n\n%d translation keys",
			escapeTranslationMarkdown(reference.Domain),
			len(keys),
		)
	default:
		return nil, nil
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

func escapeTranslationMarkdown(value string) string {
	value = strings.ReplaceAll(value, "`", "\\`")
	value = strings.ReplaceAll(value, "\r\n", " ")
	value = strings.ReplaceAll(value, "\n", " ")
	return value
}
