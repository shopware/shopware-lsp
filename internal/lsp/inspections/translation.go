package inspections

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/shopware/shopware-lsp/internal/language"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/diagnostics"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/rewrite"
	"github.com/shopware/shopware-lsp/internal/snippet"
	"github.com/shopware/shopware-lsp/internal/translation"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

const addTranslationFixID lsp.FixID = "add-missing-translation"

type addTranslationPayload struct {
	Domain string `json:"domain"`
	Key    string `json:"key"`
	File   string `json:"file"`
}

func NewTranslation(
	index *translation.Index,
	phpIndex *php.PHPIndex,
	snippetIndex *snippet.SnippetIndexer,
) lsp.Inspection {
	fix := addTranslationFix{index: index}
	return &boundInspection{
		definition: lsp.InspectionDefinition{
			ID:        "symfony.translation",
			Languages: []language.ID{language.PHP, language.Twig},
			Problems: []lsp.ProblemDefinition{
				{ID: "symfony.translation.domain.missing", Source: "symfony", DefaultSeverity: protocol.DiagnosticSeverityWarning},
				{ID: "symfony.translation.key.missing", Source: "symfony", DefaultSeverity: protocol.DiagnosticSeverityWarning},
			},
		},
		analyzer: diagnostics.NewTranslationAnalyzer(index, phpIndex, snippetIndex),
		fixes:    []lsp.QuickFix{suggestionFix{}, fix},
		bind: func(code lsp.DiagnosticID, payload map[string]any) []lsp.BoundFix {
			bound := suggestionBoundFixes(payload)
			if string(code) != "symfony.translation.key.missing" || index == nil {
				return bound
			}
			domain := mapString(payload, "domain")
			key := mapString(payload, "key")
			insertions, err := index.Insertions(domain, key)
			if err != nil {
				return bound
			}
			for _, insertion := range insertions {
				bound = append(bound, lsp.BindFix(addTranslationFixID, addTranslationPayload{
					Domain: domain,
					Key:    key,
					File:   insertion.File,
				}))
			}
			return bound
		},
	}
}

type addTranslationFix struct {
	index *translation.Index
}

func (addTranslationFix) ID() lsp.FixID { return addTranslationFixID }

func (addTranslationFix) Present(
	_ context.Context,
	fixContext lsp.FixContext,
) (lsp.FixPresentation, bool, error) {
	payload, err := lsp.DecodeBoundFixPayload[addTranslationPayload](fixContext)
	return lsp.FixPresentation{
		Title: fmt.Sprintf(
			"Symfony: Add translation '%s' to %s",
			payload.Key,
			filepath.Base(payload.File),
		),
		Kind:       protocol.CodeActionQuickFix,
		Resolution: lsp.FixLazy,
	}, payload.Domain != "" && payload.Key != "" && payload.File != "", err
}

func (f addTranslationFix) Build(
	ctx context.Context,
	fixContext lsp.FixContext,
) (rewrite.WorkspacePlan, error) {
	payload, err := lsp.DecodeBoundFixPayload[addTranslationPayload](fixContext)
	if err != nil {
		return rewrite.WorkspacePlan{}, err
	}
	if _, err := fixContext.Anchor.Resolve(
		fixContext.Document.URI,
		fixContext.Document.Version,
		fixContext.Document.SyntaxLanguage,
		fixContext.Document.SyntaxTree,
	); err != nil {
		return rewrite.WorkspacePlan{}, err
	}
	insertions, err := f.index.Insertions(payload.Domain, payload.Key)
	if err != nil {
		return rewrite.WorkspacePlan{}, err
	}
	var selected *translation.Insertion
	for index := range insertions {
		if filepath.Clean(insertions[index].File) == filepath.Clean(payload.File) {
			selected = &insertions[index]
			break
		}
	}
	if selected == nil {
		return rewrite.WorkspacePlan{}, fmt.Errorf("translation target is no longer writable")
	}
	targetURI := uriutil.FileURI(selected.File)
	target, err := fixContext.Documents.ResolveDocument(ctx, targetURI)
	if err != nil {
		return rewrite.WorkspacePlan{}, err
	}
	disk, err := os.ReadFile(selected.File)
	if err != nil || string(disk) != target.Document.Source {
		return rewrite.WorkspacePlan{}, fmt.Errorf("translation target changed")
	}
	offset := target.Document.LineIndex.OffsetUTF16(
		uint32(selected.Line),
		uint32(selected.Character),
	)
	builder := rewrite.NewBuilder(target.Document.Source)
	if err := builder.Insert(offset, selected.NewText); err != nil {
		return rewrite.WorkspacePlan{}, err
	}
	edits, err := builder.Finish()
	if err != nil {
		return rewrite.WorkspacePlan{}, err
	}
	return rewrite.WorkspacePlan{Documents: []rewrite.DocumentPlan{
		rewrite.NewDocumentPlan(
			targetURI,
			target.Version,
			target.Document.Source,
			edits,
		),
	}}, nil
}
