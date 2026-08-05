package diagnostics

import (
	"context"
	"fmt"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/serializer"
	"github.com/shopware/shopware-lsp/internal/suggestion"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

const missingSerializerClassCode lsp.DiagnosticID = "symfony.serializer.class.missing"

type SerializerAnalyzer struct {
	index    *serializer.Index
	phpIndex *php.PHPIndex
}

func NewSerializerAnalyzer(
	index *serializer.Index,
	phpIndex *php.PHPIndex,
) *SerializerAnalyzer {
	return &SerializerAnalyzer{
		index:    index,
		phpIndex: phpIndex,
	}
}

func (p *SerializerAnalyzer) Analyze(
	_ context.Context,
	document *lsp.TextDocument,
) ([]lsp.Problem, error) {
	if p == nil || p.index == nil || p.phpIndex == nil ||
		document == nil || document.SyntaxTree == nil ||
		document.SyntaxTree.Root == nil ||
		!strings.HasSuffix(strings.ToLower(document.URI), ".php") {
		return nil, nil
	}
	path, _ := uriutil.Path(document.URI)
	classNames := p.phpIndex.ClassNamesView()
	if len(classNames) == 0 {
		return nil, nil
	}
	var result []lsp.Problem
	for _, usage := range serializer.UsagesInDocument(
		path,
		document.SyntaxTree.Root,
	) {
		if usage.Kind != serializer.StringTarget {
			continue
		}
		if _, found := p.phpIndex.FindClass(usage.Class); found {
			continue
		}
		result = append(result, lsp.Problem{
			Range: usage.Range,
			Message: fmt.Sprintf(
				"Serializer target class '%s' not found",
				usage.Class,
			),
			Severity: protocol.DiagnosticSeverityWarning,
			Source:   "symfony",
			ID:       missingSerializerClassCode,
			Payload: map[string]any{
				"suggestions": suggestion.Similar(
					usage.Class,
					classNames,
				),
			},
		})
	}
	return result, nil
}
