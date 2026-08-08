package inspections

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/diagnostics"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/stretchr/testify/require"
)

func TestServicesXMLMigrationBuildsAtomicImportedFileConversion(t *testing.T) {
	root := t.TempDir()
	configDir := filepath.Join(root, "src/Resources/config")
	packagesDir := filepath.Join(configDir, "packages")
	require.NoError(t, os.MkdirAll(packagesDir, 0o755))

	servicesPath := filepath.Join(configDir, "services.xml")
	servicesSource := `<container>
    <imports>
        <import resource="packages/listeners.xml" type="xml"/>
    </imports>
    <services>
        <service id="App\Example" class="App\Example"/>
    </services>
</container>`
	listenersPath := filepath.Join(packagesDir, "listeners.xml")
	listenersSource := `<container><services>
    <service id="App\Listener" class="App\Listener"/>
</services></container>`
	require.NoError(t, os.WriteFile(servicesPath, []byte(servicesSource), 0o644))
	require.NoError(t, os.WriteFile(listenersPath, []byte(listenersSource), 0o644))

	document := lsp.NewTextDocument(uriutil.FileURI(servicesPath), servicesSource, 7)
	listenerDocument := lsp.NewTextDocument(uriutil.FileURI(listenersPath), listenersSource, 3)
	listenerVersion := listenerDocument.Version
	resolver := staticDocumentResolver{
		listenerDocument.URI: {
			Document: listenerDocument,
			Version:  &listenerVersion,
		},
	}

	inspection := NewServicesXMLMigration()
	collector := &problemCollector{}
	require.NoError(t, inspection.Inspect(context.Background(), document, collector))
	require.Len(t, collector.problems, 1)
	problem := collector.problems[0]
	require.Equal(t, diagnostics.ServicesXMLDeprecatedCode, problem.ID)
	require.Len(t, problem.Fixes, 1)
	require.Equal(t, convertServicesXMLFixID, problem.Fixes[0].ID)

	fix := quickFixWithID(t, inspection, convertServicesXMLFixID)
	plan, err := fix.Build(
		context.Background(),
		fixContext(t, document, problem, problem.Fixes[0], resolver),
	)
	require.NoError(t, err)
	require.Len(t, plan.Creates, 2)
	require.Len(t, plan.Deletes, 2)
	require.Empty(t, plan.Documents)

	created := make(map[string]string)
	for _, file := range plan.Creates {
		created[file.URI] = file.Content
	}
	require.Contains(t, created[uriutil.FileURI(filepath.Join(configDir, "services.yaml"))], "resource: packages/listeners.yaml")
	require.Contains(t, created[uriutil.FileURI(filepath.Join(configDir, "services.yaml"))], "type: yaml")
	require.Contains(t, created[uriutil.FileURI(filepath.Join(packagesDir, "listeners.yaml"))], "App\\Listener")

	deleted := make(map[string]string)
	for _, file := range plan.Deletes {
		deleted[file.URI] = file.Source
	}
	require.Equal(t, servicesSource, deleted[document.URI])
	require.Equal(t, listenersSource, deleted[listenerDocument.URI])

	wire, err := plan.WorkspaceEdit()
	require.NoError(t, err)
	require.Len(t, wire.DocumentChanges, 6)
	require.Equal(t, protocol.DeleteFileOperation, wire.DocumentChanges[4].Kind)
	require.Equal(t, protocol.DeleteFileOperation, wire.DocumentChanges[5].Kind)

	// Preparing the code action must never mutate the project itself.
	require.FileExists(t, servicesPath)
	require.FileExists(t, listenersPath)
	require.NoFileExists(t, filepath.Join(configDir, "services.yaml"))
}

func TestServicesXMLMigrationRefusesExistingYAMLTarget(t *testing.T) {
	root := t.TempDir()
	configDir := filepath.Join(root, "Resources/config")
	require.NoError(t, os.MkdirAll(configDir, 0o755))
	servicesPath := filepath.Join(configDir, "services.xml")
	servicesSource := `<container/>`
	require.NoError(t, os.WriteFile(servicesPath, []byte(servicesSource), 0o644))
	require.NoError(t, os.WriteFile(
		filepath.Join(configDir, "services.yaml"), []byte("services:\n"), 0o644,
	))

	document := lsp.NewTextDocument(uriutil.FileURI(servicesPath), servicesSource, 1)
	inspection := NewServicesXMLMigration()
	collector := &problemCollector{}
	require.NoError(t, inspection.Inspect(context.Background(), document, collector))
	require.Len(t, collector.problems, 1)
	require.Len(t, collector.problems[0].Fixes, 1)

	fix := quickFixWithID(t, inspection, convertServicesXMLFixID)
	_, err := fix.Build(
		context.Background(),
		fixContext(
			t, document, collector.problems[0],
			collector.problems[0].Fixes[0], nil,
		),
	)
	require.ErrorContains(t, err, "services.yaml exists already")
}

func TestServicesXMLMigrationDoesNotOfferLossyConversion(t *testing.T) {
	root := t.TempDir()
	document := lsp.NewTextDocument(
		uriutil.FileURI(filepath.Join(root, "Resources/config/services.xml")),
		`<container><services><stack id="app"/></services></container>`,
		1,
	)
	inspection := NewServicesXMLMigration()
	collector := &problemCollector{}
	require.NoError(t, inspection.Inspect(context.Background(), document, collector))
	require.Len(t, collector.problems, 1)
	require.Empty(t, collector.problems[0].Fixes)
}
