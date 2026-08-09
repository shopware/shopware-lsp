package diagnostics

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/asset"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/suggestion"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

type assetDiagnosticsCatalog struct {
	assets     []string
	encore     []string
	packages   []string
	importmaps []string
	vite       []string
	assetic    []string
}

func (c assetDiagnosticsCatalog) empty() bool {
	return len(c.assets) == 0 && len(c.encore) == 0 &&
		len(c.packages) == 0 && len(c.importmaps) == 0 &&
		len(c.vite) == 0 && len(c.assetic) == 0
}

type assetDiagnosticsRun struct {
	provider          *AssetAnalyzer
	document          *lsp.TextDocument
	extension         string
	validationContext context.Context
	catalog           assetDiagnosticsCatalog
}

func newAssetDiagnosticsRun(
	ctx context.Context,
	document *lsp.TextDocument,
	provider *AssetAnalyzer,
) (*assetDiagnosticsRun, error) {
	if provider == nil || provider.index == nil || document == nil ||
		document.SyntaxTree == nil || document.SyntaxTree.Root == nil {
		return nil, nil
	}
	extension := strings.ToLower(filepath.Ext(document.URI))
	if extension != ".php" && extension != ".twig" {
		return nil, nil
	}
	names, err := provider.index.NameCatalog()
	if err != nil {
		return nil, err
	}
	packages, err := provider.index.PackageNames()
	if err != nil {
		return nil, err
	}
	assetic, err := provider.index.AsseticNamedNames()
	if err != nil {
		return nil, err
	}
	run := &assetDiagnosticsRun{
		provider:          provider,
		document:          document,
		extension:         extension,
		validationContext: ctx,
		catalog: assetDiagnosticsCatalog{
			assets:     names.Assets,
			encore:     names.EncoreEntries,
			packages:   packages,
			importmaps: names.ImportmapEntries,
			vite:       names.ViteEntries,
			assetic:    assetic,
		},
	}
	if run.catalog.empty() {
		return nil, nil
	}
	if extension == ".php" && provider.phpIndex != nil {
		path, _ := uriutil.Path(document.URI)
		run.validationContext = provider.phpIndex.AddDocumentContext(
			ctx,
			path,
			document.Version,
			document.SyntaxTree.Root,
			document.SyntaxTree.Root,
		)
	}
	return run, nil
}

func (r *assetDiagnosticsRun) analyze() ([]lsp.Problem, error) {
	var result []lsp.Problem
	for _, reference := range asset.References(
		r.document.URI,
		r.document.SyntaxTree.Root,
	) {
		if !r.validReference(reference) {
			continue
		}
		resolution, err := r.resolve(reference)
		if err != nil {
			return nil, err
		}
		if resolution.skip || resolution.found ||
			nonActionableHTMLAsset(reference, resolution.suggestions) {
			continue
		}
		result = append(result, lsp.Problem{
			Range:    asset.ReferenceRange(reference),
			Message:  resolution.message,
			Severity: protocol.DiagnosticSeverityWarning,
			Source:   "symfony",
			ID:       resolution.code,
			Payload: map[string]any{
				"suggestions": resolution.suggestions,
			},
		})
	}
	return result, nil
}

func (r *assetDiagnosticsRun) validReference(reference asset.Reference) bool {
	if reference.Name == "" {
		return false
	}
	return r.extension != ".php" || asset.ValidatePHPReference(
		r.validationContext,
		reference,
		r.provider.phpIndex,
		r.document.Text,
	)
}

type assetReferenceResolution struct {
	found       bool
	skip        bool
	suggestions []string
	code        lsp.DiagnosticID
	message     string
}

func (r *assetDiagnosticsRun) resolve(
	reference asset.Reference,
) (assetReferenceResolution, error) {
	switch reference.Kind {
	case asset.EncoreEntryReference:
		return r.resolveIndexedEntry(
			reference.Name,
			asset.EncoreEntry,
			r.catalog.encore,
			missingEncoreEntryCode,
			"Webpack Encore entry '%s' not found",
		)
	case asset.ImportmapReference:
		return r.resolveImportmap(reference.Name)
	case asset.ViteEntryReference:
		return r.resolveIndexedEntry(
			reference.Name,
			asset.ViteEntry,
			r.catalog.vite,
			missingViteEntryCode,
			"Vite entry '%s' not found",
		)
	case asset.AsseticNamedReference:
		return r.resolveAsseticNamed(reference.Name)
	case asset.AssetPackageReference:
		return r.resolvePackage(reference.Name)
	default:
		return r.resolveAsset(reference)
	}
}

func (r *assetDiagnosticsRun) resolveIndexedEntry(
	name string,
	kind asset.Kind,
	names []string,
	code lsp.DiagnosticID,
	message string,
) (assetReferenceResolution, error) {
	resources, err := r.provider.index.Find(name, kind)
	return assetReferenceResolution{
		found:       len(resources) != 0,
		suggestions: suggestion.Similar(name, names),
		code:        code,
		message:     fmt.Sprintf(message, name),
	}, err
}

func (r *assetDiagnosticsRun) resolveImportmap(
	name string,
) (assetReferenceResolution, error) {
	resources, err := r.provider.index.FindImportmapEntrypoint(name)
	return assetReferenceResolution{
		found:       len(resources) != 0,
		suggestions: suggestion.Similar(name, r.catalog.importmaps),
		code:        missingImportmapCode,
		message:     fmt.Sprintf("AssetMapper entrypoint '%s' not found", name),
	}, err
}

func (r *assetDiagnosticsRun) resolveAsseticNamed(
	name string,
) (assetReferenceResolution, error) {
	resources, err := r.provider.index.FindAsseticNamed(name)
	suggestions := suggestion.Similar(name, r.catalog.assetic)
	for index := range suggestions {
		suggestions[index] = "@" + suggestions[index]
	}
	return assetReferenceResolution{
		found:       len(resources) != 0,
		suggestions: suggestions,
		code:        missingAssetCode,
		message:     fmt.Sprintf("Assetic named asset '@%s' not found", name),
	}, err
}

func (r *assetDiagnosticsRun) resolvePackage(
	name string,
) (assetReferenceResolution, error) {
	packages, err := r.provider.index.FindPackages(name)
	return assetReferenceResolution{
		found:       len(packages) != 0,
		suggestions: suggestion.Similar(name, r.catalog.packages),
		code:        missingAssetPackageCode,
		message:     fmt.Sprintf("Symfony asset package '%s' not found", name),
	}, err
}

func (r *assetDiagnosticsRun) resolveAsset(
	reference asset.Reference,
) (assetReferenceResolution, error) {
	resolution := assetReferenceResolution{
		code:    missingAssetCode,
		message: fmt.Sprintf("Symfony asset '%s' not found", reference.Name),
	}
	if reference.Package != "" && !reference.Assetic {
		packages, err := r.provider.index.FindPackages(reference.Package)
		if err != nil {
			return resolution, err
		}
		if len(packages) == 0 {
			resolution.skip = true
			return resolution, nil
		}
	}
	resources, names, err := r.assetResourcesAndNames(reference)
	if err != nil {
		return resolution, err
	}
	resolution.found = len(resources) != 0
	resolution.suggestions = suggestion.Similar(reference.Name, names)
	if reference.Assetic && reference.Package != "" {
		prefix := reference.Package + "/Resources/public/"
		for index := range resolution.suggestions {
			resolution.suggestions[index] = prefix + resolution.suggestions[index]
		}
	}
	return resolution, nil
}

func (r *assetDiagnosticsRun) assetResourcesAndNames(
	reference asset.Reference,
) ([]asset.Resource, []string, error) {
	if reference.Assetic {
		resources, err := r.provider.index.FindAsseticAssets(
			reference.Name,
			reference.Package,
		)
		if err != nil {
			return nil, nil, err
		}
		names, err := r.provider.index.AsseticNames(reference.Package)
		return resources, names, err
	}
	resources, err := r.provider.index.FindAssetsForPackage(
		reference.Name,
		reference.Package,
	)
	if err != nil {
		return nil, nil, err
	}
	names, err := r.provider.index.NamesForPackage(reference.Package)
	return resources, names, err
}

func nonActionableHTMLAsset(reference asset.Reference, suggestions []string) bool {
	// Raw HTML paths can be generated by runtime endpoints (for example a
	// development proxy) even when they look like static files. Report only
	// actionable HTML typos with a nearby indexed asset.
	return reference.HTMLType != asset.HTMLAssetNone && !reference.Assetic &&
		len(suggestions) == 0
}
