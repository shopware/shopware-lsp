package codelens

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/asset"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

type ViteCodeLensProvider struct {
	index *asset.Index
}

func NewViteCodeLensProvider(
	index *asset.Index,
) *ViteCodeLensProvider {
	return &ViteCodeLensProvider{index: index}
}

func (p *ViteCodeLensProvider) GetCodeLenses(
	ctx context.Context,
	request *lsp.CodeLensRequest,
) ([]protocol.CodeLens, error) {
	if p == nil || p.index == nil || request == nil ||
		request.CodeLensParams == nil || request.Document == nil {
		return nil, nil
	}
	path, err := uriutil.Path(request.TextDocument.URI)
	if err != nil {
		return nil, err
	}
	extension := strings.ToLower(filepath.Ext(path))
	if extension != ".js" && extension != ".ts" {
		return nil, nil
	}
	entries, err := p.index.ViteEntriesForTarget(path)
	if err != nil {
		return nil, err
	}
	var result []protocol.CodeLens
	for _, entry := range entries {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		targets := []string{relatedTarget(
			entry.File,
			relatedSourceLine(entry.File, entry.Range.Start),
		)}
		usages, usageErr := p.index.Usages(
			entry.Name,
			asset.ViteEntryReference,
		)
		if usageErr != nil {
			return nil, usageErr
		}
		for _, usage := range usages {
			targets = append(targets, relatedTarget(
				usage.File,
				relatedSourceLine(usage.File, usage.Range.Start),
			))
		}
		targets = uniqueRelatedTargets(targets)
		title := fmt.Sprintf("Vite entry '%s'", entry.Name)
		if len(targets) != 0 {
			title += fmt.Sprintf(" · %d related", len(targets))
		}
		result = append(result, relatedLens(
			protocol.Range{},
			title,
			targets,
		))
	}
	return result, nil
}

func (p *ViteCodeLensProvider) ResolveCodeLens(
	_ context.Context,
	lens *protocol.CodeLens,
) (*protocol.CodeLens, error) {
	return lens, nil
}
