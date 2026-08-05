package diagnostics

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/asset"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/suggestion"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

const (
	missingAssetCode        lsp.DiagnosticID = "symfony.asset.missing"
	missingAssetPackageCode lsp.DiagnosticID = "symfony.asset.package.missing"
	missingEncoreEntryCode  lsp.DiagnosticID = "symfony.encore.entry.missing"
	missingImportmapCode    lsp.DiagnosticID = "symfony.asset_mapper.entrypoint.missing"
	missingViteEntryCode    lsp.DiagnosticID = "symfony.vite.entry.missing"
)

type AssetAnalyzer struct {
	index    *asset.Index
	phpIndex *php.PHPIndex
}

func NewAssetAnalyzer(
	index *asset.Index,
	phpIndex *php.PHPIndex,
) *AssetAnalyzer {
	return &AssetAnalyzer{
		index:    index,
		phpIndex: phpIndex,
	}
}

func (p *AssetAnalyzer) Analyze(
	ctx context.Context,
	document *lsp.TextDocument,
) ([]lsp.Problem, error) {
	if p == nil || p.index == nil || document == nil ||
		document.SyntaxTree == nil || document.SyntaxTree.Root == nil {
		return nil, nil
	}
	extension := strings.ToLower(filepath.Ext(document.URI))
	if extension != ".php" && extension != ".twig" {
		return nil, nil
	}
	catalog, err := p.index.NameCatalog()
	if err != nil {
		return nil, err
	}
	assetNames := catalog.Assets
	entryNames := catalog.EncoreEntries
	packageNames, err := p.index.PackageNames()
	if err != nil {
		return nil, err
	}
	importmapNames := catalog.ImportmapEntries
	viteNames := catalog.ViteEntries
	asseticNames, err := p.index.AsseticNamedNames()
	if err != nil {
		return nil, err
	}
	if len(assetNames) == 0 && len(entryNames) == 0 &&
		len(importmapNames) == 0 &&
		len(viteNames) == 0 &&
		len(asseticNames) == 0 &&
		len(packageNames) == 0 {
		return nil, nil
	}
	validationContext := ctx
	if extension == ".php" && p.phpIndex != nil {
		path, _ := uriutil.Path(document.URI)
		validationContext = p.phpIndex.AddDocumentContext(
			ctx,
			path,
			document.Version,
			document.SyntaxTree.Root,
			document.SyntaxTree.Root,
		)
	}
	var result []lsp.Problem
	for _, reference := range asset.References(
		document.URI,
		document.SyntaxTree.Root,
	) {
		if reference.Name == "" {
			continue
		}
		if extension == ".php" && !asset.ValidatePHPReference(
			validationContext,
			reference,
			p.phpIndex,
			document.Text,
		) {
			continue
		}
		var found bool
		var suggestions []string
		code := missingAssetCode
		message := fmt.Sprintf(
			"Symfony asset '%s' not found",
			reference.Name,
		)
		switch reference.Kind {
		case asset.EncoreEntryReference:
			resources, findErr := p.index.Find(
				reference.Name,
				asset.EncoreEntry,
			)
			if findErr != nil {
				return nil, findErr
			}
			found = len(resources) != 0
			suggestions = suggestion.Similar(
				reference.Name,
				entryNames,
			)
			code = missingEncoreEntryCode
			message = fmt.Sprintf(
				"Webpack Encore entry '%s' not found",
				reference.Name,
			)
		case asset.ImportmapReference:
			resources, findErr := p.index.FindImportmapEntrypoint(
				reference.Name,
			)
			if findErr != nil {
				return nil, findErr
			}
			found = len(resources) != 0
			suggestions = suggestion.Similar(
				reference.Name,
				importmapNames,
			)
			code = missingImportmapCode
			message = fmt.Sprintf(
				"AssetMapper entrypoint '%s' not found",
				reference.Name,
			)
		case asset.ViteEntryReference:
			resources, findErr := p.index.Find(
				reference.Name,
				asset.ViteEntry,
			)
			if findErr != nil {
				return nil, findErr
			}
			found = len(resources) != 0
			suggestions = suggestion.Similar(
				reference.Name,
				viteNames,
			)
			code = missingViteEntryCode
			message = fmt.Sprintf(
				"Vite entry '%s' not found",
				reference.Name,
			)
		case asset.AsseticNamedReference:
			resources, findErr := p.index.FindAsseticNamed(
				reference.Name,
			)
			if findErr != nil {
				return nil, findErr
			}
			found = len(resources) != 0
			suggestions = suggestion.Similar(
				reference.Name,
				asseticNames,
			)
			for index := range suggestions {
				suggestions[index] = "@" + suggestions[index]
			}
			message = fmt.Sprintf(
				"Assetic named asset '@%s' not found",
				reference.Name,
			)
		case asset.AssetPackageReference:
			packages, findErr := p.index.FindPackages(reference.Name)
			if findErr != nil {
				return nil, findErr
			}
			found = len(packages) != 0
			suggestions = suggestion.Similar(
				reference.Name,
				packageNames,
			)
			code = missingAssetPackageCode
			message = fmt.Sprintf(
				"Symfony asset package '%s' not found",
				reference.Name,
			)
		default:
			if reference.Package != "" && !reference.Assetic {
				packages, packageErr := p.index.FindPackages(
					reference.Package,
				)
				if packageErr != nil {
					return nil, packageErr
				}
				if len(packages) == 0 {
					continue
				}
			}
			var resources []asset.Resource
			var findErr error
			if reference.Assetic {
				resources, findErr = p.index.FindAsseticAssets(
					reference.Name,
					reference.Package,
				)
			} else {
				resources, findErr = p.index.FindAssetsForPackage(
					reference.Name,
					reference.Package,
				)
			}
			if findErr != nil {
				return nil, findErr
			}
			found = len(resources) != 0
			var packageAssetNames []string
			var namesErr error
			if reference.Assetic {
				packageAssetNames, namesErr = p.index.AsseticNames(
					reference.Package,
				)
			} else {
				packageAssetNames, namesErr = p.index.NamesForPackage(
					reference.Package,
				)
			}
			if namesErr != nil {
				return nil, namesErr
			}
			suggestions = suggestion.Similar(
				reference.Name,
				packageAssetNames,
			)
			if reference.Assetic && reference.Package != "" {
				prefix := reference.Package + "/Resources/public/"
				for index := range suggestions {
					suggestions[index] = prefix + suggestions[index]
				}
			}
		}
		if found {
			continue
		}
		// Raw HTML paths can be generated by runtime endpoints (for example a
		// development proxy) even when they look like static files. Report
		// only actionable HTML typos with a nearby indexed asset.
		if reference.HTMLType != asset.HTMLAssetNone &&
			!reference.Assetic &&
			len(suggestions) == 0 {
			continue
		}
		result = append(result, lsp.Problem{
			Range:    asset.ReferenceRange(reference),
			Message:  message,
			Severity: protocol.DiagnosticSeverityWarning,
			Source:   "symfony",
			ID:       code,
			Payload: map[string]any{
				"suggestions": suggestions,
			},
		})
	}
	return result, nil
}
