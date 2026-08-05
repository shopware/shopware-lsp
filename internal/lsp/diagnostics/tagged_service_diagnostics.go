package diagnostics

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/symfony"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

const taggedServiceTypeCode lsp.DiagnosticID = "symfony.service.tag_type"

type TaggedServiceAnalyzer struct {
	phpIndex *php.PHPIndex
}

func NewTaggedServiceAnalyzer(
	phpIndex *php.PHPIndex,
) *TaggedServiceAnalyzer {
	return &TaggedServiceAnalyzer{phpIndex: phpIndex}
}

func (p *TaggedServiceAnalyzer) Analyze(
	ctx context.Context,
	document *lsp.TextDocument,
) ([]lsp.Problem, error) {
	if p == nil || p.phpIndex == nil || document == nil ||
		document.SyntaxTree == nil || document.SyntaxTree.Root == nil {
		return nil, nil
	}
	path, _ := uriutil.Path(document.URI)
	var services []symfony.Service
	var err error
	switch strings.ToLower(filepath.Ext(document.URI)) {
	case ".xml":
		services, _, err = symfony.ParseXMLServicesTree(
			path,
			document.SyntaxTree,
			document.LineIndex,
		)
	case ".yaml", ".yml":
		services, _, err = symfony.ParseYAMLServicesTree(
			path,
			document.SyntaxTree,
			document.LineIndex,
		)
	default:
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	snapshot := p.phpIndex.SemanticSnapshot()
	var result []lsp.Problem
	for _, service := range services {
		if ctx.Err() != nil {
			return nil, nil
		}
		className := strings.TrimPrefix(
			strings.TrimSpace(service.Class),
			"\\",
		)
		if className == "" || strings.Contains(className, "%") {
			continue
		}
		if _, found := p.phpIndex.FindClass(className); !found {
			continue
		}
		tags := make([]string, 0, len(service.Tags))
		for tag := range service.Tags {
			tags = append(tags, tag)
		}
		sort.Strings(tags)
		for _, tag := range tags {
			expected := symfony.RequiredServiceTagTypes(tag)
			if len(expected) == 0 {
				continue
			}
			missing := ""
			for _, target := range expected {
				if _, available := p.phpIndex.FindClass(target); !available {
					continue
				}
				missing = target
				if snapshot.IsSubtypeOf(className, target) {
					missing = ""
					break
				}
			}
			if missing == "" {
				continue
			}
			rng := service.ClassRange
			if rng.Len() == 0 {
				rng = service.IDRange
			}
			result = append(result, lsp.Problem{
				Range: rng,
				Message: fmt.Sprintf(
					"Class needs to implement '%s' for tag '%s'",
					strings.TrimPrefix(missing, "\\"),
					tag,
				),
				Source:   "symfony",
				Severity: protocol.DiagnosticSeverityWarning,
				ID:       taggedServiceTypeCode,
			})
		}
	}
	return result, nil
}
