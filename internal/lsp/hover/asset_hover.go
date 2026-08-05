package hover

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/asset"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/php"
)

type AssetHoverProvider struct {
	root     string
	index    *asset.Index
	phpIndex *php.PHPIndex
}

func NewAssetHoverProvider(
	root string,
	index *asset.Index,
	phpIndex *php.PHPIndex,
) *AssetHoverProvider {
	return &AssetHoverProvider{
		root:     root,
		index:    index,
		phpIndex: phpIndex,
	}
}

func (p *AssetHoverProvider) GetHover(
	ctx context.Context,
	request *lsp.HoverRequest,
) (*protocol.Hover, error) {
	if p == nil || p.index == nil || request == nil ||
		request.Node == nil {
		return nil, nil
	}
	extension := strings.ToLower(filepath.Ext(request.TextDocument.URI))
	if extension != ".php" && extension != ".twig" {
		return nil, nil
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
		return nil, nil
	}
	var resources []asset.Resource
	var err error
	title := "Symfony asset"
	switch reference.Kind {
	case asset.EncoreEntryReference:
		resources, err = p.index.Find(reference.Name, asset.EncoreEntry)
		title = "Webpack Encore entry"
	case asset.ImportmapReference:
		resources, err = p.index.FindImportmapEntrypoint(reference.Name)
		title = "Symfony AssetMapper entrypoint"
	case asset.ViteEntryReference:
		resources, err = p.index.Find(reference.Name, asset.ViteEntry)
		title = "Symfony Vite entry"
	case asset.AsseticNamedReference:
		resources, err = p.index.FindAsseticNamed(reference.Name)
		title = "Assetic named asset"
	case asset.AssetPackageReference:
		packages, packageErr := p.index.FindPackages(reference.Name)
		if packageErr != nil || len(packages) == 0 {
			return nil, packageErr
		}
		return p.packageHover(reference, packages, request), nil
	default:
		if reference.Assetic {
			resources, err = p.index.FindAsseticAssets(
				reference.Name,
				reference.Package,
			)
			title = "Assetic file"
		} else {
			resources, err = p.index.FindAssetsForPackage(
				reference.Name,
				reference.Package,
			)
		}
		if reference.HTMLType != asset.HTMLAssetNone &&
			!reference.Assetic {
			title = "Twig HTML asset"
		}
	}
	if err != nil || len(resources) == 0 {
		return nil, err
	}
	var markdown strings.Builder
	fmt.Fprintf(
		&markdown,
		"**%s** `%s`",
		title,
		escapeAssetMarkdown(reference.Name),
	)
	for _, resource := range resources {
		target := resource.Target
		if target == "" {
			target = resource.File
		}
		display, pathErr := filepath.Rel(p.root, target)
		if pathErr != nil {
			display = target
		}
		fmt.Fprintf(
			&markdown,
			"\n\n- %s · `%s`",
			resource.Kind.String(),
			escapeAssetMarkdown(filepath.ToSlash(display)),
		)
		if resource.Version != "" {
			fmt.Fprintf(
				&markdown,
				" · version `%s`",
				escapeAssetMarkdown(resource.Version),
			)
		}
		if resource.URL != "" {
			fmt.Fprintf(
				&markdown,
				" · `%s`",
				escapeAssetMarkdown(resource.URL),
			)
		}
	}
	rng := asset.ReferenceRange(reference)
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

func (p *AssetHoverProvider) packageHover(
	reference asset.Reference,
	packages []asset.Package,
	request *lsp.HoverRequest,
) *protocol.Hover {
	var markdown strings.Builder
	fmt.Fprintf(
		&markdown,
		"**Symfony asset package** `%s`",
		escapeAssetMarkdown(reference.Name),
	)
	seen := make(map[string]struct{})
	for _, current := range packages {
		description := "configured package"
		if current.Inferred {
			description = "bundle package"
		}
		if current.BasePath != "" {
			description += " · `" +
				escapeAssetMarkdown(current.BasePath) + "`"
		}
		target := current.File
		if relative, err := filepath.Rel(p.root, target); err == nil {
			target = filepath.ToSlash(relative)
		}
		line := "\n\n- " + description
		if target != "" {
			line += " · `" + escapeAssetMarkdown(target) + "`"
		}
		if _, duplicate := seen[line]; duplicate {
			continue
		}
		seen[line] = struct{}{}
		markdown.WriteString(line)
	}
	rng := asset.ReferenceRange(reference)
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
	}
}

func escapeAssetMarkdown(value string) string {
	return strings.ReplaceAll(value, "`", "\\`")
}
