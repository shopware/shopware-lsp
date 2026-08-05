package diagnostics

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/symfony"
)

const missingContainerConstantCode lsp.DiagnosticID = "symfony.constant.missing"

type ContainerConstantAnalyzer struct {
	phpIndex *php.PHPIndex
}

func NewContainerConstantAnalyzer(
	phpIndex *php.PHPIndex,
) *ContainerConstantAnalyzer {
	return &ContainerConstantAnalyzer{phpIndex: phpIndex}
}

func (p *ContainerConstantAnalyzer) Analyze(
	ctx context.Context,
	document *lsp.TextDocument,
) ([]lsp.Problem, error) {
	if p == nil || p.phpIndex == nil || document == nil ||
		document.SyntaxTree == nil || document.SyntaxTree.Root == nil {
		return nil, nil
	}
	references := containerConstantReferences(document)
	var result []lsp.Problem
	for _, reference := range references {
		if ctx.Err() != nil {
			return nil, nil
		}
		if len(symfony.ResolveContainerConstant(
			p.phpIndex,
			reference.Name,
		)) != 0 {
			continue
		}
		result = append(result, lsp.Problem{
			Range:    reference.Range,
			Message:  "Symfony: constant not found",
			Source:   "symfony",
			Severity: protocol.DiagnosticSeverityError,
			ID:       missingContainerConstantCode,
		})
	}
	return result, nil
}

func containerConstantReferences(
	document *lsp.TextDocument,
) []symfony.ContainerConstantReference {
	if document == nil {
		return nil
	}
	switch strings.ToLower(filepath.Ext(document.URI)) {
	case ".yaml", ".yml":
		return symfony.YAMLContainerConstantReferences(document.Text)
	case ".xml":
		if document.SyntaxTree == nil {
			return nil
		}
		return symfony.XMLContainerConstantReferences(
			document.SyntaxTree.Root,
		)
	default:
		return nil
	}
}
