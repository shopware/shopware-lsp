package reference

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	assetdomain "github.com/shopware/shopware-lsp/internal/asset"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

type AssetReferenceProvider struct {
	index    *assetdomain.Index
	phpIndex *php.PHPIndex
}

func NewAssetReferenceProvider(
	index *assetdomain.Index,
	phpIndex *php.PHPIndex,
) *AssetReferenceProvider {
	return &AssetReferenceProvider{
		index:    index,
		phpIndex: phpIndex,
	}
}

func (p *AssetReferenceProvider) GetReferences(
	ctx context.Context,
	request *lsp.ReferenceRequest,
) ([]protocol.Location, error) {
	if p == nil || p.index == nil || request == nil ||
		request.Node == nil || request.Document == nil {
		return nil, nil
	}
	extension := strings.ToLower(filepath.Ext(request.TextDocument.URI))
	if extension != ".php" && extension != ".twig" {
		return nil, nil
	}
	reference, found := assetdomain.ReferenceAt(
		request.TextDocument.URI,
		request.Node,
	)
	if !found || extension == ".php" &&
		!assetdomain.ValidatePHPReference(
			ctx,
			reference,
			p.phpIndex,
			request.DocumentContent,
		) {
		return nil, nil
	}
	currentPath, _ := uriutil.Path(request.TextDocument.URI)
	usages, err := p.index.UsagesForPackage(
		reference.Name,
		reference.Kind,
		reference.Package,
	)
	if err != nil {
		return nil, err
	}
	filtered := make([]assetdomain.Usage, 0, len(usages))
	for _, usage := range usages {
		if usage.File != currentPath {
			filtered = append(filtered, usage)
		}
	}
	for _, current := range assetdomain.References(
		currentPath,
		request.Root,
	) {
		if current.Kind == reference.Kind &&
			strings.EqualFold(current.Name, reference.Name) &&
			strings.EqualFold(current.Package, reference.Package) {
			filtered = append(filtered, assetdomain.Usage{
				Name:    current.Name,
				Package: current.Package,
				Kind:    current.Kind,
				File:    currentPath,
				Range:   assetdomain.ReferenceRange(current),
			})
		}
	}
	var result []protocol.Location
	seen := make(map[string]struct{})
	if request.Context.IncludeDeclaration {
		if reference.Kind == assetdomain.AssetPackageReference {
			packages, packageErr := p.index.FindPackages(reference.Name)
			if packageErr != nil {
				return nil, packageErr
			}
			for _, location := range assetPackageReferenceLocations(packages) {
				key := assetReferenceLocationKey(location)
				if _, exists := seen[key]; exists {
					continue
				}
				seen[key] = struct{}{}
				result = append(result, location)
			}
		}
		resources, resourceErr := p.referenceResources(reference)
		if resourceErr != nil {
			return nil, resourceErr
		}
		for _, resource := range resources {
			location, locationFound := assetDeclarationLocation(resource)
			if !locationFound {
				continue
			}
			key := assetReferenceLocationKey(location)
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			result = append(result, location)
			if reference.Kind == assetdomain.ViteEntryReference &&
				resource.File != "" && resource.Range.Len() != 0 {
				configLocation, configFound := assetFileRangeLocation(
					resource.File,
					resource.Range,
				)
				if configFound {
					configKey := assetReferenceLocationKey(configLocation)
					if _, exists := seen[configKey]; !exists {
						seen[configKey] = struct{}{}
						result = append(result, configLocation)
					}
				}
			}
		}
	}
	for _, usage := range filtered {
		location, locationFound := assetUsageLocation(
			usage,
			currentPath,
			request,
		)
		if !locationFound {
			continue
		}
		key := assetReferenceLocationKey(location)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, location)
	}
	return result, nil
}

func (p *AssetReferenceProvider) referenceResources(
	reference assetdomain.Reference,
) ([]assetdomain.Resource, error) {
	if reference.Kind == assetdomain.EncoreEntryReference {
		return p.index.Find(reference.Name, assetdomain.EncoreEntry)
	}
	if reference.Kind == assetdomain.ImportmapReference {
		return p.index.FindImportmapEntrypoint(reference.Name)
	}
	if reference.Kind == assetdomain.ViteEntryReference {
		return p.index.Find(reference.Name, assetdomain.ViteEntry)
	}
	if reference.Kind == assetdomain.AsseticNamedReference {
		return p.index.FindAsseticNamed(reference.Name)
	}
	if reference.Kind == assetdomain.AssetPackageReference {
		return nil, nil
	}
	if reference.Assetic {
		return p.index.FindAsseticAssets(
			reference.Name,
			reference.Package,
		)
	}
	return p.index.FindAssetsForPackage(
		reference.Name,
		reference.Package,
	)
}

func assetPackageReferenceLocations(
	packages []assetdomain.Package,
) []protocol.Location {
	var result []protocol.Location
	for _, current := range packages {
		if current.File == "" {
			continue
		}
		location, found := assetFileRangeLocation(
			current.File,
			current.Range,
		)
		if !found && current.Range.Len() == 0 {
			location = protocol.Location{
				URI: uriutil.FileURI(current.File),
			}
			found = true
		}
		if found {
			result = append(result, location)
		}
	}
	return result
}

func assetDeclarationLocation(
	resource assetdomain.Resource,
) (protocol.Location, bool) {
	if resource.Target != "" {
		if info, err := os.Stat(resource.Target); err == nil &&
			!info.IsDir() {
			return protocol.Location{
				URI: uriutil.FileURI(resource.Target),
			}, true
		}
	}
	if resource.File == "" {
		return protocol.Location{}, false
	}
	return assetFileRangeLocation(resource.File, resource.Range)
}

func assetUsageLocation(
	usage assetdomain.Usage,
	currentPath string,
	request *lsp.ReferenceRequest,
) (protocol.Location, bool) {
	if usage.File == currentPath {
		return protocol.Location{
			URI: request.TextDocument.URI,
			Range: assetReferenceRange(
				usage.Range,
				request.LineIndex,
			),
		}, true
	}
	return assetFileRangeLocation(usage.File, usage.Range)
}

func assetFileRangeLocation(
	path string,
	rng cst.TextRange,
) (protocol.Location, bool) {
	source, err := os.ReadFile(path)
	if err != nil {
		return protocol.Location{}, false
	}
	return protocol.Location{
		URI: uriutil.FileURI(path),
		Range: assetReferenceRange(
			rng,
			cst.NewLineIndex(string(source)),
		),
	}, true
}

func assetReferenceRange(
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

func assetReferenceLocationKey(location protocol.Location) string {
	return fmt.Sprintf(
		"%s:%d:%d:%d:%d",
		location.URI,
		location.Range.Start.Line,
		location.Range.Start.Character,
		location.Range.End.Line,
		location.Range.End.Character,
	)
}
