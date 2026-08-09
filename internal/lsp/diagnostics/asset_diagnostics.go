package diagnostics

import (
	"context"

	"github.com/shopware/shopware-lsp/internal/asset"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/php"
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
	run, err := newAssetDiagnosticsRun(ctx, document, p)
	if err != nil || run == nil {
		return nil, err
	}
	return run.analyze()
}
