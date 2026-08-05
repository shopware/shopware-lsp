package inspections

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/language"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/diagnostics"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/rewrite"
	"github.com/shopware/shopware-lsp/internal/twig"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

const createTemplateFixID lsp.FixID = "create-template"

type createTemplatePayload struct {
	Name string `json:"name"`
	URI  string `json:"uri"`
}

func NewTemplate(
	root string,
	twigIndex *twig.TwigIndexer,
	phpIndex *php.PHPIndex,
) lsp.Inspection {
	return &boundInspection{
		definition: lsp.InspectionDefinition{
			ID:        "twig.template",
			Languages: []language.ID{language.PHP, language.Twig},
			Problems: []lsp.ProblemDefinition{{
				ID:              "twig.template.missing",
				Source:          "twig",
				DefaultSeverity: protocol.DiagnosticSeverityError,
			}},
		},
		analyzer: diagnostics.NewTemplateAnalyzer(twigIndex, phpIndex),
		fixes:    []lsp.QuickFix{suggestionFix{}, createTemplateFix{}},
		bind: func(_ lsp.DiagnosticID, payload map[string]any) []lsp.BoundFix {
			bound := suggestionBoundFixes(payload)
			name, _ := payload["templateName"].(string)
			target, ok := safeTemplateCreationTarget(root, name)
			if !ok {
				return bound
			}
			return append(bound, lsp.BindFix(createTemplateFixID, createTemplatePayload{
				Name: name,
				URI:  uriutil.FileURI(target),
			}))
		},
	}
}

type createTemplateFix struct{}

func (createTemplateFix) ID() lsp.FixID { return createTemplateFixID }

func (createTemplateFix) Present(
	_ context.Context,
	fixContext lsp.FixContext,
) (lsp.FixPresentation, bool, error) {
	payload, err := lsp.DecodeBoundFixPayload[createTemplatePayload](fixContext)
	if err != nil || payload.Name == "" || payload.URI == "" {
		return lsp.FixPresentation{}, false, err
	}
	return lsp.FixPresentation{
		Title:      fmt.Sprintf("Symfony: Create template '%s'", payload.Name),
		Kind:       protocol.CodeActionQuickFix,
		Resolution: lsp.FixEager,
	}, true, nil
}

func (createTemplateFix) Build(
	_ context.Context,
	fixContext lsp.FixContext,
) (rewrite.WorkspacePlan, error) {
	payload, err := lsp.DecodeBoundFixPayload[createTemplatePayload](fixContext)
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
	return rewrite.WorkspacePlan{Creates: []rewrite.CreateFilePlan{{
		URI: payload.URI,
	}}}, nil
}

func safeTemplateCreationTarget(root, name string) (string, bool) {
	if strings.TrimSpace(root) == "" {
		return "", false
	}
	name = filepath.ToSlash(strings.TrimSpace(name))
	name = strings.TrimPrefix(name, "/")
	if name == "" || strings.HasPrefix(name, "@") ||
		strings.ContainsAny(name, `:\`) ||
		!strings.HasSuffix(strings.ToLower(name), ".twig") {
		return "", false
	}
	clean := path.Clean(name)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", false
	}
	rootPath, err := filepath.Abs(root)
	if err != nil {
		return "", false
	}
	target := filepath.Join(rootPath, "templates", filepath.FromSlash(clean))
	if !pathInside(rootPath, target) {
		return "", false
	}
	if _, err := os.Stat(target); err == nil || !errors.Is(err, os.ErrNotExist) {
		return "", false
	}
	canonicalRoot, err := filepath.EvalSymlinks(rootPath)
	if err != nil {
		return "", false
	}
	existing := filepath.Dir(target)
	for {
		if _, err = os.Stat(existing); err == nil {
			break
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", false
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			return "", false
		}
		existing = parent
	}
	canonicalExisting, err := filepath.EvalSymlinks(existing)
	if err != nil {
		return "", false
	}
	remainder, err := filepath.Rel(existing, target)
	if err != nil {
		return "", false
	}
	canonicalTarget := filepath.Join(canonicalExisting, remainder)
	if !pathInside(canonicalRoot, canonicalTarget) {
		return "", false
	}
	return target, true
}

func pathInside(root, target string) bool {
	relative, err := filepath.Rel(root, target)
	return err == nil && relative != ".." &&
		!strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
