package inspections

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/shopware/shopware-lsp/internal/language"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/diagnostics"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/rewrite"
	"github.com/shopware/shopware-lsp/internal/symfony"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

const convertServicesXMLFixID lsp.FixID = "convert-services-xml-to-yaml"

func NewServicesXMLMigration() lsp.Inspection {
	fix := servicesXMLMigrationFix{}
	return &boundInspection{
		definition: lsp.InspectionDefinition{
			ID:        "symfony.services_xml_migration",
			Languages: []language.ID{language.XML},
			Problems: []lsp.ProblemDefinition{{
				ID: diagnostics.ServicesXMLDeprecatedCode, Source: "symfony",
				DefaultSeverity: protocol.DiagnosticSeverityWarning,
			}},
		},
		analyzer: diagnostics.NewServicesXMLDeprecationAnalyzer(),
		fixes:    []lsp.QuickFix{fix},
		bind: func(_ lsp.DiagnosticID, payload map[string]any) []lsp.BoundFix {
			convertible, _ := payload["convertible"].(bool)
			if !convertible {
				return nil
			}
			return []lsp.BoundFix{lsp.BindFix(
				convertServicesXMLFixID,
				diagnostics.ServicesXMLDeprecationPayload{Convertible: true},
			)}
		},
	}
}

type servicesXMLMigrationFix struct{}

func (servicesXMLMigrationFix) ID() lsp.FixID { return convertServicesXMLFixID }

func (servicesXMLMigrationFix) Present(
	_ context.Context,
	fixContext lsp.FixContext,
) (lsp.FixPresentation, bool, error) {
	payload, err := lsp.DecodeBoundFixPayload[diagnostics.ServicesXMLDeprecationPayload](fixContext)
	return lsp.FixPresentation{
		Title:      "Symfony: Convert services.xml to services.yaml",
		Kind:       protocol.CodeActionQuickFix,
		Preferred:  true,
		Resolution: lsp.FixLazy,
	}, payload.Convertible, err
}

func (servicesXMLMigrationFix) Build(
	ctx context.Context,
	fixContext lsp.FixContext,
) (rewrite.WorkspacePlan, error) {
	payload, err := lsp.DecodeBoundFixPayload[diagnostics.ServicesXMLDeprecationPayload](fixContext)
	if err != nil {
		return rewrite.WorkspacePlan{}, err
	}
	if !payload.Convertible {
		return rewrite.WorkspacePlan{}, fmt.Errorf("services.xml cannot be converted safely")
	}
	if _, err := fixContext.Anchor.Resolve(
		fixContext.Document.URI,
		fixContext.Document.Version,
		fixContext.Document.SyntaxLanguage,
		fixContext.Document.SyntaxTree,
	); err != nil {
		return rewrite.WorkspacePlan{}, err
	}

	entryPath, err := uriutil.Path(fixContext.Document.URI)
	if err != nil {
		return rewrite.WorkspacePlan{}, err
	}
	entryPath = filepath.Clean(entryPath)
	entryVersion := fixContext.Document.Version
	snapshots := map[string]lsp.DocumentSnapshot{
		entryPath: {
			Document: fixContext.Document,
			Version:  &entryVersion,
		},
	}
	read := func(ctx context.Context, path string) ([]byte, error) {
		path = filepath.Clean(path)
		if snapshot, found := snapshots[path]; found {
			return []byte(snapshot.Document.Source), nil
		}
		if fixContext.Documents == nil {
			return nil, fmt.Errorf("document resolver is unavailable")
		}
		snapshot, resolveErr := fixContext.Documents.ResolveDocument(
			ctx, uriutil.FileURI(path),
		)
		if resolveErr != nil {
			return nil, resolveErr
		}
		if snapshot.Document == nil {
			return nil, fmt.Errorf("document %s is unavailable", path)
		}
		if snapshot.Version != nil {
			version := *snapshot.Version
			snapshot.Version = &version
		}
		snapshots[path] = snapshot
		return []byte(snapshot.Document.Source), nil
	}
	targetExists := func(path string) (bool, error) {
		_, statErr := os.Stat(path)
		switch {
		case statErr == nil:
			return true, nil
		case errors.Is(statErr, os.ErrNotExist):
			return false, nil
		default:
			return false, statErr
		}
	}

	conversions, err := symfony.PlanServicesXMLConversion(
		ctx, entryPath, read, targetExists,
	)
	if err != nil {
		return rewrite.WorkspacePlan{}, err
	}

	plan := rewrite.WorkspacePlan{}
	for _, conversion := range conversions {
		snapshot, found := snapshots[filepath.Clean(conversion.SourcePath)]
		if !found || snapshot.Document == nil {
			return rewrite.WorkspacePlan{}, fmt.Errorf(
				"source document %s is unavailable", conversion.SourcePath,
			)
		}
		plan.Creates = append(plan.Creates, rewrite.CreateFilePlan{
			URI:     uriutil.FileURI(conversion.TargetPath),
			Content: string(conversion.Content),
		})
		plan.Deletes = append(plan.Deletes, rewrite.DeleteFilePlan{
			URI:     uriutil.FileURI(conversion.SourcePath),
			Version: snapshot.Version,
			Source:  snapshot.Document.Source,
		})
	}
	return plan, nil
}
