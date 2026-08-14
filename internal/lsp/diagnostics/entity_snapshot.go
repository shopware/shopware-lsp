package diagnostics

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/shopware/entityschema"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

type EntitySnapshotAnalyzer struct {
	entities *entityschema.IndexedCatalog
}

func NewEntitySnapshotAnalyzer(catalogs ...*entityschema.IndexedCatalog) *EntitySnapshotAnalyzer {
	analyzer := &EntitySnapshotAnalyzer{}
	if len(catalogs) != 0 {
		analyzer.entities = catalogs[0]
	}
	return analyzer
}

func (a *EntitySnapshotAnalyzer) Analyze(ctx context.Context, document *lsp.TextDocument) ([]lsp.Problem, error) {
	if document == nil || !strings.HasSuffix(strings.ToLower(document.URI), ".snapshot.json") || !strings.Contains(filepath.ToSlash(document.URI), "/src/Resources/shopware-lsp/schema/") {
		return nil, nil
	}
	fullRange := cst.TextRange{Start: 0, End: uint32(len(document.Source))}
	problem := func(id lsp.DiagnosticID, message string, severity protocol.DiagnosticSeverity) lsp.Problem {
		return lsp.Problem{Range: fullRange, ID: id, Message: message, Source: "shopware-lsp", Severity: severity}
	}
	current, err := entityschema.ParseSnapshot(document.Text)
	if err != nil {
		return []lsp.Problem{problem("shopware.entity_snapshot.invalid", err.Error(), protocol.DiagnosticSeverityError)}, nil
	}
	path, err := uriutil.Path(document.URI)
	if err != nil {
		return nil, err
	}
	pluginRoot, ok := snapshotPluginRoot(path)
	if !ok {
		return nil, nil
	}
	files, err := entityschema.ReadSnapshots(pluginRoot)
	if err != nil {
		return []lsp.Problem{problem("shopware.entity_snapshot.invalid", err.Error(), protocol.DiagnosticSeverityError)}, nil
	}
	// The current editor buffer may be newer than disk. Substitute it before
	// graph validation so saves do not produce a transient stale-ID report.
	for index := range files {
		if filepath.Clean(files[index].Path) == filepath.Clean(path) {
			files[index].Snapshot = current
		}
	}
	graph, err := entityschema.BuildSnapshotGraph(files)
	if err != nil {
		return []lsp.Problem{problem("shopware.entity_snapshot.graph", err.Error(), protocol.DiagnosticSeverityError)}, nil
	}
	var problems []lsp.Problem
	if missing := graph.Missing[current.ID]; len(missing) != 0 {
		problems = append(problems, problem("shopware.entity_snapshot.parent_missing", "Snapshot parents are missing: "+strings.Join(missing, ", "), protocol.DiagnosticSeverityError))
	}
	if len(graph.Leaves) > 1 {
		if current.ID == graph.Leaves[0].Snapshot.ID {
			problems = append(problems, problem("shopware.entity_snapshot.reconcile", fmt.Sprintf("Snapshot history has %d leaves and must be reconciled", len(graph.Leaves)), protocol.DiagnosticSeverityError))
		}
		return problems, nil
	}
	if len(graph.Leaves) != 1 {
		return problems, nil
	}
	leaf := graph.Leaves[0].Snapshot
	if current.ID != leaf.ID {
		return problems, nil
	}
	checkedMigrations := make(map[string]struct{})
	for _, snapshotFile := range graph.Files {
		for _, migration := range snapshotFile.Snapshot.Migrations {
			if _, checked := checkedMigrations[migration.Path]; checked {
				continue
			}
			checkedMigrations[migration.Path] = struct{}{}
			content, readErr := os.ReadFile(filepath.Join(pluginRoot, filepath.FromSlash(migration.Path)))
			if errors.Is(readErr, os.ErrNotExist) {
				problems = append(problems, problem("shopware.entity_snapshot.migration_missing", "Referenced migration is missing: "+migration.Path, protocol.DiagnosticSeverityError))
				continue
			}
			if readErr != nil {
				return nil, readErr
			}
			if entityschema.FileSHA256(content) != migration.SHA256 {
				problems = append(problems, problem("shopware.entity_snapshot.migration_changed", "Referenced migration changed after the snapshot was committed: "+migration.Path, protocol.DiagnosticSeverityError))
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var scanned entityschema.Schema
	var scanErr error
	if a.entities != nil {
		scanned, _, scanErr = a.entities.ScanContext(ctx, pluginRoot, nil)
	} else {
		// Focused importer tests do not construct a production workspace index.
		scanned, _, scanErr = entityschema.ScanPluginSchema(pluginRoot)
	}
	if errors.Is(scanErr, entityschema.ErrIndexNotReady) {
		return problems, nil
	}
	if scanErr != nil {
		return nil, scanErr
	}
	scanned = entityschema.RestoreSnapshotOnlyIndexes(scanned, leaf.Schema)
	left, _ := json.Marshal(scanned.Normalize())
	right, _ := json.Marshal(leaf.Schema.Normalize())
	if string(left) != string(right) {
		problems = append(problems, problem("shopware.entity_snapshot.schema_drift", "Entity definitions differ from the latest committed schema snapshot", protocol.DiagnosticSeverityWarning))
	}
	return problems, nil
}

func snapshotPluginRoot(path string) (string, bool) {
	directory := filepath.Dir(path)
	if filepath.Base(directory) != "schema" || filepath.Base(filepath.Dir(directory)) != "shopware-lsp" || filepath.Base(filepath.Dir(filepath.Dir(directory))) != "Resources" || filepath.Base(filepath.Dir(filepath.Dir(filepath.Dir(directory)))) != "src" {
		return "", false
	}
	return filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(directory)))), true
}
