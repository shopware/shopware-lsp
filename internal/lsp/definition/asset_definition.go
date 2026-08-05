package definition

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/asset"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

type AssetDefinitionProvider struct {
	index    *asset.Index
	phpIndex *php.PHPIndex
}

func NewAssetDefinitionProvider(
	index *asset.Index,
	phpIndex *php.PHPIndex,
) *AssetDefinitionProvider {
	return &AssetDefinitionProvider{
		index:    index,
		phpIndex: phpIndex,
	}
}

func (p *AssetDefinitionProvider) GetDefinition(
	ctx context.Context,
	request *lsp.DefinitionRequest,
) []protocol.Location {
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
	var resources []asset.Resource
	var err error
	switch reference.Kind {
	case asset.EncoreEntryReference:
		resources, err = p.index.Find(
			reference.Name,
			asset.EncoreEntry,
		)
	case asset.ImportmapReference:
		resources, err = p.index.FindImportmapEntrypoint(
			reference.Name,
		)
	case asset.ViteEntryReference:
		resources, err = p.index.Find(reference.Name, asset.ViteEntry)
	case asset.AsseticNamedReference:
		resources, err = p.index.FindAsseticNamed(reference.Name)
	case asset.AssetPackageReference:
		packages, packageErr := p.index.FindPackages(reference.Name)
		if packageErr != nil {
			return nil
		}
		return assetPackageLocations(packages)
	default:
		if reference.Assetic {
			resources, err = p.index.FindAsseticAssets(
				reference.Name,
				reference.Package,
			)
		} else {
			resources, err = p.index.FindAssetsForPackage(
				reference.Name,
				reference.Package,
			)
		}
	}
	if err != nil {
		return nil
	}
	return assetResourceLocations(resources)
}

func assetPackageLocations(
	packages []asset.Package,
) []protocol.Location {
	var result []protocol.Location
	seen := make(map[string]struct{})
	for _, current := range packages {
		if current.File == "" {
			continue
		}
		location := protocol.Location{
			URI: uriutil.FileURI(current.File),
		}
		if source, err := os.ReadFile(current.File); err == nil &&
			current.Range.Len() != 0 {
			location.Range = assetProtocolRange(
				current.Range,
				cst.NewLineIndex(string(source)),
			)
		}
		key := location.URI + ":" + current.Range.String()
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, location)
	}
	return result
}

func assetResourceLocations(
	resources []asset.Resource,
) []protocol.Location {
	var result []protocol.Location
	seen := make(map[string]struct{})
	for _, resource := range resources {
		if resource.Target != "" {
			if info, err := os.Stat(resource.Target); err == nil &&
				!info.IsDir() {
				location := protocol.Location{
					URI: uriutil.FileURI(resource.Target),
				}
				if _, exists := seen[location.URI]; !exists {
					seen[location.URI] = struct{}{}
					result = append(result, location)
				}
			}
		}
		if resource.File == "" ||
			resource.Range.Len() == 0 ||
			resource.File == resource.Target {
			continue
		}
		location := assetResourceLocation(resource)
		key := location.URI + ":" + resource.Range.String()
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, location)
	}
	return result
}

func assetResourceLocation(resource asset.Resource) protocol.Location {
	location := protocol.Location{URI: uriutil.FileURI(resource.File)}
	source, err := os.ReadFile(resource.File)
	if err != nil || resource.Range.Len() == 0 {
		return location
	}
	location.Range = assetProtocolRange(
		resource.Range,
		cst.NewLineIndex(string(source)),
	)
	return location
}

func assetProtocolRange(
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
