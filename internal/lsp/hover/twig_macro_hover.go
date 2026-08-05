package hover

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/twig"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

type TwigMacroHoverProvider struct {
	root  string
	index *twig.TwigIndexer
}

func NewTwigMacroHoverProvider(
	root string,
	index *twig.TwigIndexer,
) *TwigMacroHoverProvider {
	return &TwigMacroHoverProvider{root: root, index: index}
}

func (p *TwigMacroHoverProvider) GetHover(
	_ context.Context,
	request *lsp.HoverRequest,
) (*protocol.Hover, error) {
	if p == nil || p.index == nil || request == nil ||
		request.Node == nil || request.Root == nil ||
		request.LineIndex == nil ||
		strings.ToLower(filepath.Ext(request.TextDocument.URI)) != ".twig" {
		return nil, nil
	}
	offset := request.LineIndex.OffsetUTF16(
		uint32(request.Position.Line),
		uint32(request.Position.Character),
	)
	path, _ := uriutil.Path(request.TextDocument.URI)
	reference, found := twig.MacroReferenceAt(
		path,
		request.Root,
		request.Node,
		offset,
	)
	if !found {
		return nil, nil
	}
	macros := p.macros(path, request.Root, reference)
	if len(macros) == 0 {
		return nil, nil
	}
	macro := macros[0]
	var markdown strings.Builder
	fmt.Fprintf(
		&markdown,
		"**Twig macro** `%s`",
		escapeTwigMacroMarkdown(macro.Signature()),
	)
	display := macro.FilePath
	if relative, err := filepath.Rel(p.root, macro.FilePath); err == nil {
		display = relative
	}
	if display != "" {
		fmt.Fprintf(
			&markdown,
			"\n\nDeclared in `%s`",
			escapeTwigMacroMarkdown(filepath.ToSlash(display)),
		)
	}
	usageCount := 0
	for _, template := range reference.Templates {
		usages, err := p.index.GetMacroUsages(template, reference.Name)
		if err == nil {
			usageCount += len(usages)
		}
	}
	if usageCount != 0 {
		fmt.Fprintf(&markdown, "\n\n%d indexed use(s)", usageCount)
	}
	return &protocol.Hover{
		Contents: protocol.MarkupContent{
			Kind:  protocol.Markdown,
			Value: markdown.String(),
		},
		Range: securityProtocolRange(reference.Range, request.LineIndex),
	}, nil
}

func (p *TwigMacroHoverProvider) macros(
	path string,
	root *cst.Node,
	reference twig.MacroReference,
) []twig.Macro {
	var result []twig.Macro
	for _, template := range reference.Templates {
		macros, err := p.index.FindMacro(template, reference.Name)
		if err == nil {
			result = append(result, macros...)
		}
	}
	currentTemplates := twig.TemplateNames(path)
	for _, template := range reference.Templates {
		if !containsTwigMacroName(currentTemplates, template) {
			continue
		}
		filtered := result[:0]
		for _, macro := range result {
			if macro.FilePath != path {
				filtered = append(filtered, macro)
			}
		}
		result = filtered
		for _, macro := range twig.MacrosInDocument(path, root) {
			if strings.EqualFold(macro.Name, reference.Name) {
				result = append(result, macro)
			}
		}
		break
	}
	return result
}

func containsTwigMacroName(values []string, candidate string) bool {
	for _, value := range values {
		if value == candidate {
			return true
		}
	}
	return false
}

func escapeTwigMacroMarkdown(value string) string {
	return strings.ReplaceAll(value, "`", "\\`")
}
