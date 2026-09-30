package inspections

import (
	"context"
	"strings"

	"github.com/shopware/shopware-lsp/internal/admin/dataset"
	"github.com/shopware/shopware-lsp/internal/extension"
	"github.com/shopware/shopware-lsp/internal/language"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/diagnostics"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/rewrite"
	"github.com/shopware/shopware-lsp/internal/shopware/dal"
)

const addDatasetReadPermissionsFixID lsp.FixID = "add-app-dataset-read-permissions"

type datasetPermissionPayload struct {
	Entities []string `json:"entities"`
	Manifest string   `json:"manifest"`
}

func NewAdminDataset(
	datasets *dataset.Index,
	definitions *dal.Index,
	extensions *extension.ExtensionIndexer,
) lsp.Inspection {
	return &boundInspection{
		definition: lsp.InspectionDefinition{
			ID: "shopware.admin.dataset",
			Languages: []language.ID{
				language.JavaScript,
				language.Vue,
			},
			Problems: []lsp.ProblemDefinition{
				{
					ID:              "admin.dataset.permission-missing",
					Source:          "shopware-lsp",
					DefaultSeverity: protocol.DiagnosticSeverityError,
				},
				{
					ID:              "admin.dataset.unscoped-subscription",
					Source:          "shopware-lsp",
					DefaultSeverity: protocol.DiagnosticSeverityWarning,
				},
				{
					ID:              "admin.dataset.unresolved",
					Source:          "shopware-lsp",
					DefaultSeverity: protocol.DiagnosticSeverityHint,
				},
			},
		},
		analyzer: diagnostics.NewAdminDatasetAnalyzer(datasets, definitions, extensions),
		fixes:    []lsp.QuickFix{datasetReadPermissionFix{}},
		bind: func(code lsp.DiagnosticID, payload map[string]any) []lsp.BoundFix {
			if code != "admin.dataset.permission-missing" {
				return nil
			}
			value := datasetPermissionPayload{
				Entities: mapStrings(payload, "entities"),
				Manifest: mapString(payload, "manifest"),
			}
			if len(value.Entities) == 0 || value.Manifest == "" {
				return nil
			}
			return []lsp.BoundFix{lsp.BindFix(addDatasetReadPermissionsFixID, value)}
		},
	}
}

type datasetReadPermissionFix struct{}

func (datasetReadPermissionFix) ID() lsp.FixID { return addDatasetReadPermissionsFixID }

func (datasetReadPermissionFix) Present(
	_ context.Context,
	fixContext lsp.FixContext,
) (lsp.FixPresentation, bool, error) {
	payload, err := lsp.DecodeBoundFixPayload[datasetPermissionPayload](fixContext)
	if err != nil || len(payload.Entities) == 0 || payload.Manifest == "" {
		return lsp.FixPresentation{}, false, err
	}
	title := "Add read permission for '" + payload.Entities[0] + "'"
	if len(payload.Entities) > 1 {
		quoted := make([]string, len(payload.Entities))
		for index, entity := range payload.Entities {
			quoted[index] = "'" + entity + "'"
		}
		title = "Add read permissions for " + strings.Join(quoted, ", ")
	}
	return lsp.FixPresentation{
		Title:      title,
		Kind:       protocol.CodeActionQuickFix,
		Preferred:  true,
		Resolution: lsp.FixLazy,
	}, true, nil
}

func (datasetReadPermissionFix) Build(
	ctx context.Context,
	fixContext lsp.FixContext,
) (rewrite.WorkspacePlan, error) {
	payload, err := lsp.DecodeBoundFixPayload[datasetPermissionPayload](fixContext)
	if err != nil {
		return rewrite.WorkspacePlan{}, err
	}
	return planAppReadPermissions(ctx, fixContext, payload.Manifest, payload.Entities)
}
