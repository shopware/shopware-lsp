package inspections

import (
	"github.com/shopware/shopware-lsp/internal/extension"
	"github.com/shopware/shopware-lsp/internal/language"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/diagnostics"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	shopwaredal "github.com/shopware/shopware-lsp/internal/shopware/dal"
)

// NewAppDataset validates Meteor Admin SDK dataset selectors in app
// administration sources against the owning app manifest.xml read
// permissions. Missing privileges are errors with a quick fix that adds the
// `<read>` entry; subscriptions without selectors are warnings because the
// whole dataset is sent.
func NewAppDataset(
	extensions *extension.ExtensionIndexer,
	dal *shopwaredal.Index,
) lsp.Inspection {
	return &boundInspection{
		definition: lsp.InspectionDefinition{
			ID: "shopware.app_dataset",
			Languages: []language.ID{
				language.JavaScript,
				language.Vue,
			},
			Problems: []lsp.ProblemDefinition{
				{ID: "app_dataset.permission-missing", Source: "shopware-lsp", DefaultSeverity: protocol.DiagnosticSeverityError},
				{ID: "app_dataset.subscribe-without-selectors", Source: "shopware-lsp", DefaultSeverity: protocol.DiagnosticSeverityWarning},
			},
		},
		analyzer: diagnostics.NewAppDatasetAnalyzer(extensions, dal),
		fixes:    []lsp.QuickFix{appReadPermissionFix{}},
		bind: func(code lsp.DiagnosticID, payload map[string]any) []lsp.BoundFix {
			if code != "app_dataset.permission-missing" {
				return nil
			}
			value := appPermissionPayload{
				Entity:   mapString(payload, "entity"),
				Manifest: mapString(payload, "manifest"),
			}
			if value.Entity == "" || value.Manifest == "" {
				return nil
			}
			return []lsp.BoundFix{lsp.BindFix(addAppReadPermissionFixID, value)}
		},
	}
}
