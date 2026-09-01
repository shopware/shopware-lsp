package hover

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/security"
)

type SecurityHoverProvider struct {
	root  string
	index *security.Index
}

func NewSecurityHoverProvider(
	root string,
	index *security.Index,
) *SecurityHoverProvider {
	return &SecurityHoverProvider{root: root, index: index}
}

func (p *SecurityHoverProvider) GetHover(
	ctx context.Context,
	request *lsp.HoverRequest,
) (*protocol.Hover, error) {
	if p == nil || p.index == nil || request == nil ||
		request.Node == nil || request.LineIndex == nil {
		return nil, nil
	}
	offset := request.LineIndex.OffsetUTF16(
		uint32(request.Position.Line),
		uint32(request.Position.Character),
	)
	if option, found := security.ConfigOptionAt(request.Node); found {
		return &protocol.Hover{
			Contents: protocol.MarkupContent{
				Kind: protocol.Markdown,
				Value: fmt.Sprintf(
					"**Symfony security option** `%s`\n\n%s",
					escapeSecurityMarkdown(option.Name),
					option.Detail,
				),
			},
			Range: securityProtocolRange(
				request.Node.RangeTrimmedTrivia(),
				request.LineIndex,
			),
		}, nil
	}
	if current, found := security.ConfigReferenceAt(
		request.Node,
	); found {
		return p.configHover(request, current)
	}
	reference, ok := security.ReferenceAt(
		ctx,
		request.TextDocument.URI,
		request.Root,
		request.Node,
		request.SourceString(),
		offset,
	)
	if !ok {
		return nil, nil
	}
	attribute, found, err := p.index.Attribute(reference.Name)
	if err != nil || !found || len(attribute.Declarations()) == 0 {
		return nil, err
	}

	var markdown strings.Builder
	fmt.Fprintf(
		&markdown,
		"**Symfony security attribute** `%s`",
		escapeSecurityMarkdown(attribute.Name),
	)
	declaration := attribute.Declarations()[0]
	switch declaration.Origin {
	case security.OriginVoter:
		if declaration.Class != "" {
			fmt.Fprintf(
				&markdown,
				"\n\nSupported by voter `%s`",
				escapeSecurityMarkdown(declaration.Class),
			)
		}
	case security.OriginRoleHierarchy:
		markdown.WriteString("\n\nDeclared in `security.role_hierarchy`")
	case security.OriginAccessControl:
		markdown.WriteString("\n\nUsed by `security.access_control`")
	case security.OriginBuiltIn:
		markdown.WriteString("\n\nBuilt-in Symfony security attribute")
	}
	if declaration.File != "" {
		display := declaration.File
		if relative, relativeErr := filepath.Rel(
			p.root,
			declaration.File,
		); relativeErr == nil {
			display = relative
		}
		fmt.Fprintf(
			&markdown,
			"\n\nSource: `%s`",
			escapeSecurityMarkdown(filepath.ToSlash(display)),
		)
	}
	if count := len(attribute.References()); count != 0 {
		fmt.Fprintf(&markdown, "\n\n%d indexed use(s)", count)
	}

	return &protocol.Hover{
		Contents: protocol.MarkupContent{
			Kind:  protocol.Markdown,
			Value: markdown.String(),
		},
		Range: securityProtocolRange(reference.Range, request.LineIndex),
	}, nil
}

func (p *SecurityHoverProvider) configHover(
	request *lsp.HoverRequest,
	current security.ConfigOccurrence,
) (*protocol.Hover, error) {
	symbol, found, err := p.index.ConfigSymbol(current.Name, current.Kind)
	if err != nil {
		return nil, err
	}
	if !found {
		symbol = security.ConfigSymbol{
			Name: current.Name,
			Kind: current.Kind,
		}
	}
	title := "Symfony user provider"
	if current.Kind == security.ConfigFirewall {
		title = "Symfony firewall"
	}
	var markdown strings.Builder
	fmt.Fprintf(
		&markdown,
		"**%s** `%s`",
		title,
		escapeSecurityMarkdown(current.Name),
	)
	if declarations := symbol.Declarations(); len(declarations) != 0 {
		declaration := declarations[0]
		display := declaration.File
		if relative, relativeErr := filepath.Rel(
			p.root,
			declaration.File,
		); relativeErr == nil {
			display = relative
		}
		if display != "" {
			fmt.Fprintf(
				&markdown,
				"\n\nDeclared in `%s`",
				escapeSecurityMarkdown(filepath.ToSlash(display)),
			)
		}
	}
	if count := len(symbol.References()); count != 0 {
		fmt.Fprintf(&markdown, "\n\n%d indexed use(s)", count)
	}
	return &protocol.Hover{
		Contents: protocol.MarkupContent{
			Kind:  protocol.Markdown,
			Value: markdown.String(),
		},
		Range: securityProtocolRange(
			current.Range,
			request.LineIndex,
		),
	}, nil
}

func securityProtocolRange(
	rng cst.TextRange,
	lineIndex *cst.LineIndex,
) *protocol.Range {
	startLine, startCharacter := lineIndex.PositionUTF16(rng.Start)
	endLine, endCharacter := lineIndex.PositionUTF16(rng.End)
	return &protocol.Range{
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

func escapeSecurityMarkdown(value string) string {
	return strings.ReplaceAll(value, "`", "\\`")
}
