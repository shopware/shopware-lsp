package callhierarchy

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/admin"
	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAdminCallHierarchyConnectsInheritedMethodsAcrossJavaScriptAndTwig(
	t *testing.T,
) {
	fixture := newAdminCallHierarchyFixture(t)
	provider := NewAdminCallHierarchyProvider(fixture.index)

	saveItems, liveBase := prepareAdminCallHierarchy(
		t, provider, fixture.basePath, fixture.liveBase, "save(id:",
	)
	require.Len(t, saveItems, 1)
	save := saveItems[0]
	save = roundTripAdminCallHierarchyItem(t, save)
	assert.Equal(t, "save", save.Name)
	assert.Contains(t, save.Detail, "sw-base")
	assert.Contains(t, save.Detail, "sw-child")

	liveTemplate := lsp.NewTextDocument(
		uriutil.FileURI(fixture.childTemplate),
		`<button @click="save(product.id); save(other.id)">Save</button>`,
		2,
	)
	incoming, err := provider.IncomingCalls(
		context.Background(),
		&lsp.CallHierarchyCallsRequest{
			Item: save, Documents: []*lsp.TextDocument{
				liveBase, liveTemplate,
			},
		},
	)
	require.NoError(t, err)
	require.Len(t, incoming, 2)
	callers := adminIncomingCallsByName(incoming)
	require.Contains(t, callers, "submit")
	require.Contains(t, callers, "sw-child template")
	assert.Len(t, callers["submit"].FromRanges, 2)
	assert.Len(t, callers["sw-child template"].FromRanges, 2)

	submitItems, _ := prepareAdminCallHierarchy(
		t, provider, fixture.basePath, fixture.liveBase, "submit(id:",
	)
	require.Len(t, submitItems, 1)
	outgoing, err := provider.OutgoingCalls(
		context.Background(),
		&lsp.CallHierarchyCallsRequest{
			Item: submitItems[0], Documents: []*lsp.TextDocument{liveBase},
		},
	)
	require.NoError(t, err)
	require.Len(t, outgoing, 2)
	targets := adminOutgoingCallsByName(outgoing)
	require.Contains(t, targets, "save")
	require.Contains(t, targets, "login")
	assert.Len(t, targets["save"].FromRanges, 2)
	assert.Len(t, targets["login"].FromRanges, 1)
}

func TestAdminCallHierarchySupportsPiniaActionDeclarationsAndCallers(
	t *testing.T,
) {
	fixture := newAdminCallHierarchyFixture(t)
	provider := NewAdminCallHierarchyProvider(fixture.index)
	loginItems, liveStore := prepareAdminCallHierarchy(
		t, provider, fixture.storePath, fixture.storeSource, "login(user:",
	)
	require.Len(t, loginItems, 1)
	login := loginItems[0]
	assert.Equal(t, "login", login.Name)
	assert.Contains(t, login.Detail, "session")

	liveBase := lsp.NewTextDocument(
		uriutil.FileURI(fixture.basePath), fixture.liveBase, 2,
	)
	incoming, err := provider.IncomingCalls(
		context.Background(),
		&lsp.CallHierarchyCallsRequest{
			Item: login, Documents: []*lsp.TextDocument{
				liveStore, liveBase,
			},
		},
	)
	require.NoError(t, err)
	callers := adminIncomingCallsByName(incoming)
	require.Contains(t, callers, "submit")
	require.Contains(t, callers, "refresh")
	assert.Len(t, callers["submit"].FromRanges, 1)
	assert.Len(t, callers["refresh"].FromRanges, 1)

	refreshItems, _ := prepareAdminCallHierarchy(
		t, provider, fixture.storePath, fixture.storeSource, "refresh(user:",
	)
	require.Len(t, refreshItems, 1)
	outgoing, err := provider.OutgoingCalls(
		context.Background(),
		&lsp.CallHierarchyCallsRequest{
			Item: refreshItems[0], Documents: []*lsp.TextDocument{liveStore},
		},
	)
	require.NoError(t, err)
	require.Len(t, outgoing, 1)
	assert.Equal(t, "login", outgoing[0].To.Name)
	assert.Len(t, outgoing[0].FromRanges, 1)
}

type adminCallHierarchyFixture struct {
	index         *admin.AdminComponentIndexer
	basePath      string
	childTemplate string
	storePath     string
	liveBase      string
	storeSource   string
}

func newAdminCallHierarchyFixture(t *testing.T) adminCallHierarchyFixture {
	t.Helper()
	root := t.TempDir()
	idx, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	adminRoot := filepath.Join(
		root, "src/Administration/Resources/app/administration/src",
	)
	basePath := filepath.Join(adminRoot, "component/sw-base/index.ts")
	baseTemplate := filepath.Join(
		filepath.Dir(basePath), "sw-base.html.twig",
	)
	childPath := filepath.Join(adminRoot, "component/sw-child/index.ts")
	childTemplate := filepath.Join(
		filepath.Dir(childPath), "sw-child.html.twig",
	)
	storePath := filepath.Join(adminRoot, "store/session.ts")
	persistedBase := `import template from './sw-base.html.twig';
Component.register('sw-base', {
    template,
    methods: {
        save(id: string): void {},
        submit(id: string): void {
            this.save(id);
            Shopware.Store.get('session').login(id);
        },
    },
});`
	liveBase := strings.Replace(
		persistedBase,
		"            this.save(id);",
		"            this.save(id);\n            this.save(id);",
		1,
	)
	storeSource := `Shopware.Store.register('session', {
    actions: {
        login(user: string): void {},
        refresh(user: string): void {
            Shopware.Store.get('session').login(user);
        },
    },
});`
	for path, source := range map[string]string{
		basePath:      persistedBase,
		baseTemplate:  `{{ save(product.id) }}`,
		childPath:     `import template from './sw-child.html.twig'; Component.extend('sw-child', 'sw-base', { template });`,
		childTemplate: `<button @click="save(product.id)">Save</button>`,
		storePath:     storeSource,
	} {
		require.NoError(t, idx.Index(indexer.NewParsedFile(
			path, []byte(source),
		)))
	}
	return adminCallHierarchyFixture{
		index: idx, basePath: basePath, childTemplate: childTemplate,
		storePath: storePath, liveBase: liveBase, storeSource: storeSource,
	}
}

func prepareAdminCallHierarchy(
	t *testing.T,
	provider *AdminCallHierarchyProvider,
	path,
	source,
	needle string,
) ([]protocol.CallHierarchyItem, *lsp.TextDocument) {
	t.Helper()
	document := lsp.NewTextDocument(uriutil.FileURI(path), source, 2)
	offset := uint32(strings.Index(source, needle) + 1)
	require.Greater(t, int(offset), 0)
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.CallHierarchyPrepareParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: document.URI},
		Position: protocol.Position{
			Line: int(line), Character: int(character),
		},
	}
	items, err := provider.PrepareCallHierarchy(
		context.Background(),
		&lsp.CallHierarchyPrepareRequest{
			CallHierarchyPrepareParams: params,
			SyntaxContext: lsp.SyntaxContext{
				Document: document, Language: document.SyntaxLanguage,
				DocumentContent: document.Text,
				DocumentTree:    document.SyntaxTree,
				LineIndex:       document.LineIndex,
				Root:            document.SyntaxTree.Root,
				Node:            document.SyntaxTree.Root.NodeAtOffset(offset),
			},
		},
	)
	require.NoError(t, err)
	return items, document
}

func adminIncomingCallsByName(
	values []protocol.CallHierarchyIncomingCall,
) map[string]protocol.CallHierarchyIncomingCall {
	result := make(map[string]protocol.CallHierarchyIncomingCall, len(values))
	for _, value := range values {
		result[value.From.Name] = value
	}
	return result
}

func adminOutgoingCallsByName(
	values []protocol.CallHierarchyOutgoingCall,
) map[string]protocol.CallHierarchyOutgoingCall {
	result := make(map[string]protocol.CallHierarchyOutgoingCall, len(values))
	for _, value := range values {
		result[value.To.Name] = value
	}
	return result
}

func roundTripAdminCallHierarchyItem(
	t *testing.T,
	item protocol.CallHierarchyItem,
) protocol.CallHierarchyItem {
	t.Helper()
	payload, err := json.Marshal(item)
	require.NoError(t, err)
	var result protocol.CallHierarchyItem
	require.NoError(t, json.Unmarshal(payload, &result))
	return result
}
