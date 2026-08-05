package completion

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/asset"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/php"
)

type AssetCompletionProvider struct {
	index    *asset.Index
	phpIndex *php.PHPIndex
}

func NewAssetCompletionProvider(
	index *asset.Index,
	phpIndex *php.PHPIndex,
) *AssetCompletionProvider {
	return &AssetCompletionProvider{
		index:    index,
		phpIndex: phpIndex,
	}
}

func (p *AssetCompletionProvider) GetCompletions(
	ctx context.Context,
	request *lsp.CompletionRequest,
) []protocol.CompletionItem {
	if p == nil || p.index == nil || request == nil ||
		request.Node == nil {
		return nil
	}
	extension := strings.ToLower(filepath.Ext(request.TextDocument.URI))
	if extension != ".php" && extension != ".twig" {
		return nil
	}
	reference, found := asset.ReferenceAt(
		request.TextDocument.URI,
		request.Node,
	)
	if !found || extension == ".php" &&
		!asset.ValidatePHPReference(
			ctx,
			reference,
			p.phpIndex,
			request.DocumentContent,
		) {
		return nil
	}
	var names []string
	var err error
	detail := "Symfony public asset"
	switch reference.Kind {
	case asset.EncoreEntryReference:
		names, err = p.index.EntryNames()
		detail = "Webpack Encore entry"
	case asset.ImportmapReference:
		names, err = p.index.ImportmapEntryNames()
		detail = "Symfony AssetMapper entrypoint"
	case asset.ViteEntryReference:
		names, err = p.index.ViteEntryNames()
		detail = "Symfony Vite entry"
	case asset.AsseticNamedReference:
		names, err = p.index.AsseticNamedNames()
		detail = "Assetic named asset"
	case asset.AssetPackageReference:
		names, err = p.index.PackageNames()
		detail = "Symfony asset package"
	default:
		if reference.Assetic {
			names, err = p.index.AsseticNames(reference.Package)
			detail = "Assetic file"
		} else {
			names, err = p.index.NamesForPackage(reference.Package)
		}
	}
	if err != nil {
		return nil
	}
	if reference.HTMLType != asset.HTMLAssetNone &&
		reference.Kind != asset.AsseticNamedReference {
		filtered := names[:0]
		for _, name := range names {
			if asset.MatchesHTMLAssetType(name, reference.HTMLType) {
				filtered = append(filtered, name)
			}
		}
		names = filtered
		detail = "Symfony asset for Twig HTML"
	}
	items := make([]protocol.CompletionItem, 0, len(names))
	for _, name := range names {
		label := name
		if reference.Kind == asset.AsseticNamedReference {
			label = "@" + name
		} else if reference.Assetic && reference.Package != "" {
			label = reference.Package + "/Resources/public/" + name
		}
		kind := protocol.FileCompletion
		if reference.Kind == asset.AssetPackageReference ||
			reference.Kind == asset.ImportmapReference ||
			reference.Kind == asset.ViteEntryReference {
			kind = protocol.ModuleCompletion
		}
		item := protocol.CompletionItem{
			Label:  label,
			Kind:   int(kind),
			Detail: detail,
		}
		if reference.HTMLType != asset.HTMLAssetNone &&
			!reference.Assetic {
			item.FilterText = name
			item.TextEdit = protocol.TextEdit{
				Range: assetCompletionRange(
					asset.ReferenceRange(reference),
					request.LineIndex,
				),
				NewText: twigAssetExpression(reference, label),
			}
		}
		items = append(items, item)
	}
	return items
}

func assetCompletionRange(
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

func twigAssetExpression(reference asset.Reference, name string) string {
	quote := byte('\'')
	if reference.Container != nil {
		text := strings.TrimSpace(reference.Container.Text())
		if len(text) != 0 && text[0] == '\'' {
			quote = '"'
		}
	}
	escaped := strings.ReplaceAll(name, `\`, `\\`)
	if quote == '\'' {
		escaped = strings.ReplaceAll(escaped, `'`, `\'`)
	} else {
		escaped = strings.ReplaceAll(escaped, `"`, `\"`)
	}
	return "{{ asset(" + string(quote) + escaped +
		string(quote) + ") }}"
}

func (p *AssetCompletionProvider) GetTriggerCharacters() []string {
	return []string{"/", "'", "\""}
}
