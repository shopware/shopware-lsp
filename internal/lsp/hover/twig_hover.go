package hover

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/extension"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/theme"
	"github.com/shopware/shopware-lsp/internal/twig"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

type TwigHoverProvider struct {
	iconProvider *theme.IconProvider
	projectRoot  string
	phpIndex     *php.PHPIndex
	twigIndexer  *twig.TwigIndexer
}

func NewTwigHoverProvider(
	projectRoot string,
	extensionIndexer *extension.ExtensionIndexer,
	phpIndex *php.PHPIndex,
	twigIndexer *twig.TwigIndexer,
) *TwigHoverProvider {
	return &TwigHoverProvider{
		iconProvider: theme.NewIconProvider(projectRoot, extensionIndexer),
		projectRoot:  projectRoot,
		phpIndex:     phpIndex,
		twigIndexer:  twigIndexer,
	}
}

func (p *TwigHoverProvider) GetHover(ctx context.Context, params *lsp.HoverRequest) (*protocol.Hover, error) {
	// Only process .twig files
	if !strings.HasSuffix(strings.ToLower(params.TextDocument.URI), ".twig") {
		return nil, nil
	}
	if params.Node == nil {
		return nil, nil
	}

	if memberHover, err := p.twigPHPMemberHover(params); memberHover != nil || err != nil {
		return memberHover, err
	}

	if variableHover, err := p.twigVariableHover(params); variableHover != nil || err != nil {
		return variableHover, err
	}

	// Check if hovering over sw_icon
	if twigquery.StringInTag(params.Node, "sw_icon") {
		iconName := twigquery.StringValue(twigquery.LiteralStringAt(params.Node))

		// Extract icon configuration from parent node
		cfg := twigquery.HashStringMap(params.Node)

		pack, ok := cfg["pack"]
		if !ok {
			pack = "default"
		}

		// Get the icon path
		iconPath := p.iconProvider.GetIcon(pack, iconName)

		if iconPath != "" {
			// Create markdown content with icon preview
			// For VSCode and other editors, we need to use file:// URIs for local images
			var imageUri string
			if strings.HasPrefix(iconPath, "/") {
				// Absolute path - convert to file URI
				imageUri = uriutil.FileURI(iconPath)
			} else {
				// Try to create a relative path from the current document
				documentPath, err := uriutil.Path(params.TextDocument.URI)
				if err != nil {
					return nil, err
				}
				docDir := filepath.Dir(documentPath)
				relPath, err := filepath.Rel(docDir, iconPath)
				if err != nil {
					// If relative path fails, use absolute file URI
					imageUri = uriutil.FileURI(iconPath)
				} else {
					imageUri = relPath
				}
			}

			// Make display path relative to project root
			displayPath, err := filepath.Rel(p.projectRoot, iconPath)
			if err != nil {
				// If we can't make it relative, use the original path
				displayPath = iconPath
			}

			markdownContent := fmt.Sprintf("**Icon**: `%s`\n\n**Pack**: `%s`\n\n**Preview**:\n\n![%s](%s)\n\n**Path**: `%s`",
				iconName,
				pack,
				iconName,
				imageUri,
				displayPath,
			)

			return &protocol.Hover{
				Contents: protocol.MarkupContent{
					Kind:  protocol.Markdown,
					Value: markdownContent,
				},
				Range: &protocol.Range{
					Start: protocol.Position{
						Line:      params.Position.Line,
						Character: params.Position.Character,
					},
					End: protocol.Position{
						Line:      params.Position.Line,
						Character: params.Position.Character + len(iconName),
					},
				},
			}, nil
		}
	}

	return nil, nil
}

func (p *TwigHoverProvider) twigPHPMemberHover(
	request *lsp.HoverRequest,
) (*protocol.Hover, error) {
	if request == nil || request.Node == nil || request.Root == nil ||
		p.phpIndex == nil {
		return nil, nil
	}
	nameNode := twigquery.ClosestNodeOfKind(
		request.Node,
		twigsyntax.TwigLiteralName,
	)
	if nameNode == nil {
		return nil, nil
	}
	templatePath, err := uriutil.Path(request.TextDocument.URI)
	if err != nil {
		return nil, err
	}
	resolution, ok := (twig.PHPAccessResolver{
		PHP:  p.phpIndex,
		Twig: p.twigIndexer,
	}).ResolveName(templatePath, request.Root, nameNode)
	if !ok {
		return nil, nil
	}
	var sections []string
	seen := make(map[string]struct{})
	for _, member := range resolution.Members {
		symbol := member.Symbol
		if _, exists := seen[string(symbol.ID)]; exists {
			continue
		}
		seen[string(symbol.ID)] = struct{}{}
		signature := symbol.FullyQualified
		switch symbol.Kind {
		case semantic.PropertySymbol:
			signature += ": " + member.Type.String()
		case semantic.MethodSymbol:
			signature += "(): " + member.Type.String()
		case semantic.ClassConstantSymbol, semantic.EnumCaseSymbol:
			signature = p.phpIndex.ConstantSymbolName(symbol)
		}
		value := "```php\n" + signature + "\n```"
		if symbol.DocSummary != "" {
			value += "\n\n" + symbol.DocSummary
		}
		if symbol.Flags.Has(semantic.DeprecatedFlag) {
			value += "\n\n**Deprecated**"
		}
		sections = append(sections, value)
	}
	if len(sections) == 0 {
		return nil, nil
	}
	return &protocol.Hover{
		Contents: protocol.MarkupContent{
			Kind:  protocol.Markdown,
			Value: strings.Join(sections, "\n\n---\n\n"),
		},
		Range: twigVariableHoverRange(resolution.NameNode, request),
	}, nil
}

func (p *TwigHoverProvider) twigVariableHover(
	request *lsp.HoverRequest,
) (*protocol.Hover, error) {
	nameNode := twigquery.ClosestNodeOfKind(
		request.Node,
		twigsyntax.TwigLiteralName,
	)
	if nameNode == nil ||
		twigquery.ClosestNodeOfKind(nameNode, twigsyntax.TwigVar) == nil {
		return nil, nil
	}
	if accessor := twigquery.ClosestNodeOfKind(
		nameNode,
		twigsyntax.TwigAccessor,
	); accessor != nil {
		names := twigquery.Nodes(accessor, twigsyntax.TwigLiteralName)
		if len(names) == 0 || names[0] != nameNode {
			return nil, nil
		}
	}
	name := strings.TrimSpace(nameNode.Text())
	path, err := uriutil.Path(request.TextDocument.URI)
	if err != nil {
		return nil, err
	}
	if p.phpIndex != nil {
		variables, variableErr := p.phpIndex.TwigTemplateVariables(
			twig.TemplateNames(path)...,
		)
		if variableErr != nil {
			return nil, variableErr
		}
		for _, variable := range variables {
			if variable.Name != name {
				continue
			}
			displayPath, pathErr := filepath.Rel(
				p.projectRoot,
				variable.File,
			)
			if pathErr != nil {
				displayPath = variable.File
			}
			return &protocol.Hover{
				Contents: protocol.MarkupContent{
					Kind: protocol.Markdown,
					Value: fmt.Sprintf(
						"**Twig variable** `%s`\n\nPHP type: `%s`\n\nProvided by `%s` for `%s`.",
						variable.Name,
						variable.Type,
						filepath.ToSlash(displayPath),
						variable.Template,
					),
				},
				Range: twigVariableHoverRange(nameNode, request),
			}, nil
		}
	}
	if p.twigIndexer == nil {
		return nil, nil
	}
	globals, err := p.twigIndexer.GetGlobals(name)
	if err != nil || len(globals) == 0 {
		return nil, err
	}
	global := globals[0]
	typeName := global.Type
	if typeName == "" {
		typeName = "mixed"
	}
	var markdown strings.Builder
	fmt.Fprintf(
		&markdown,
		"**Twig global** `%s`\n\nPHP type: `%s`\n\nSource: %s.",
		global.Name,
		typeName,
		global.Source.String(),
	)
	if global.ServiceID != "" {
		fmt.Fprintf(
			&markdown,
			"\n\nService: `%s`",
			global.ServiceID,
		)
	}
	if global.File != "" {
		displayPath, pathErr := filepath.Rel(p.projectRoot, global.File)
		if pathErr != nil {
			displayPath = global.File
		}
		fmt.Fprintf(
			&markdown,
			"\n\nDeclared in `%s`.",
			filepath.ToSlash(displayPath),
		)
	}
	if len(globals) > 1 {
		fmt.Fprintf(
			&markdown,
			"\n\n%d indexed definitions.",
			len(globals),
		)
	}
	return &protocol.Hover{
		Contents: protocol.MarkupContent{
			Kind:  protocol.Markdown,
			Value: markdown.String(),
		},
		Range: twigVariableHoverRange(nameNode, request),
	}, nil
}

func twigVariableHoverRange(
	nameNode *twigsyntax.Node,
	request *lsp.HoverRequest,
) *protocol.Range {
	rng := nameNode.RangeTrimmedTrivia()
	startLine, startCharacter := request.LineIndex.PositionUTF16(rng.Start)
	endLine, endCharacter := request.LineIndex.PositionUTF16(rng.End)
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
