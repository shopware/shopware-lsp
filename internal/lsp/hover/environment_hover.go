package hover

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/environment"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

type EnvironmentHoverProvider struct {
	root  string
	index *environment.Index
}

func NewEnvironmentHoverProvider(
	root string,
	index *environment.Index,
) *EnvironmentHoverProvider {
	return &EnvironmentHoverProvider{root: root, index: index}
}

func (p *EnvironmentHoverProvider) GetHover(
	_ context.Context,
	request *lsp.HoverRequest,
) (*protocol.Hover, error) {
	if p == nil || p.index == nil || request == nil ||
		request.HoverParams == nil || request.LineIndex == nil {
		return nil, nil
	}
	offset := request.LineIndex.OffsetUTF16(
		uint32(request.Position.Line),
		uint32(request.Position.Character),
	)
	path, _ := uriutil.Path(request.TextDocument.URI)
	current, found := environment.PHPOccurrenceAt(
		request.Node,
		offset,
	)
	if !found {
		current, found = environment.OccurrenceAt(
			path,
			request.SourceString(),
			offset,
		)
	}
	if !found {
		return nil, nil
	}
	variable, exists, err := p.index.Variable(current.Name)
	if err != nil {
		return nil, err
	}
	if !exists {
		variable = environment.Variable{Name: current.Name}
	}

	var markdown strings.Builder
	fmt.Fprintf(
		&markdown,
		"**Symfony environment variable** `%s`",
		escapeEnvironmentMarkdown(current.Name),
	)
	fmt.Fprintf(
		&markdown,
		"\n\n%d declaration(s) · %d reference(s)",
		len(variable.Declarations),
		len(variable.References),
	)
	if len(current.Processors) != 0 {
		fmt.Fprintf(
			&markdown,
			"\n\nProcessors: `%s`",
			escapeEnvironmentMarkdown(
				strings.Join(current.Processors, " → "),
			),
		)
	}
	for _, declaration := range variable.Declarations {
		fmt.Fprintf(
			&markdown,
			"\n\n- `%s`",
			escapeEnvironmentMarkdown(
				environmentRelativePath(p.root, declaration.File),
			),
		)
		if declaration.Value != "" {
			fmt.Fprintf(
				&markdown,
				": `%s`",
				escapeEnvironmentMarkdown(
					environmentDisplayValue(
						current.Name,
						declaration.Value,
					),
				),
			)
		}
	}
	startLine, startCharacter := request.LineIndex.PositionUTF16(
		current.NameRange.Start,
	)
	endLine, endCharacter := request.LineIndex.PositionUTF16(
		current.NameRange.End,
	)
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

func environmentDisplayValue(name, value string) string {
	upper := strings.ToUpper(name)
	for _, sensitive := range []string{
		"SECRET",
		"PASSWORD",
		"PASSWD",
		"TOKEN",
		"KEY",
		"AUTH",
		"CREDENTIAL",
		"DSN",
		"DATABASE_URL",
		"PRIVATE_KEY",
		"API_KEY",
	} {
		if strings.Contains(upper, sensitive) {
			return "••••••••"
		}
	}
	if strings.Contains(value, "://") && strings.Contains(value, "@") {
		return "••••••••"
	}
	return value
}

func environmentRelativePath(root, path string) string {
	if root == "" {
		return path
	}
	relative, err := filepath.Rel(root, path)
	if err != nil || strings.HasPrefix(relative, "..") {
		return path
	}
	return filepath.ToSlash(relative)
}

func escapeEnvironmentMarkdown(value string) string {
	return strings.ReplaceAll(value, "`", "\\`")
}
