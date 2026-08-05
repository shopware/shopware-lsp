package signature

import (
	"context"
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

func TestAdminSignatureHelpForInheritedComponentMethodsInJavaScriptAndTwig(
	t *testing.T,
) {
	root := t.TempDir()
	idx, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	adminRoot := filepath.Join(
		root, "src/Administration/Resources/app/administration/src",
	)
	basePath := filepath.Join(adminRoot, "component/sw-base/index.ts")
	baseTemplate := filepath.Join(filepath.Dir(basePath), "sw-base.html.twig")
	childPath := filepath.Join(adminRoot, "component/sw-child/index.ts")
	childTemplate := filepath.Join(filepath.Dir(childPath), "sw-child.html.twig")
	baseSource := `import template from './sw-base.html.twig';
Component.register('sw-base', {
    template,
    methods: {
        save(id: string, options?: { force: boolean, tags: string[] }): Promise<void> {
            return Promise.resolve();
        },
        submit() {
            return this.save('product-id', { force: true, tags: ['a', 'b'] });
        },
    },
});`
	childSource := `import template from './sw-child.html.twig';
Component.extend('sw-child', 'sw-base', { template });`
	childTemplateSource := `<button @click="save(product.id, { force: true, tags: ['a', 'b'] })">Save</button>`
	for path, source := range map[string]string{
		basePath:      baseSource,
		baseTemplate:  `{{ save('id', { force: true, tags: [] }) }}`,
		childPath:     childSource,
		childTemplate: childTemplateSource,
	} {
		require.NoError(t, idx.Index(indexer.NewParsedFile(path, []byte(source))))
	}
	provider := NewAdminSignatureProvider(idx)

	javaScriptResult, err := provider.GetSignatureHelp(
		context.Background(),
		adminSignatureRequest(basePath, baseSource, "force: true"),
	)
	require.NoError(t, err)
	requireAdminSignature(
		t,
		javaScriptResult,
		"save(id: string, options?: { force: boolean, tags: string[] }): Promise<void>",
		1,
	)
	assert.Contains(
		t, javaScriptResult.Signatures[0].Documentation.Value,
		"sw-base",
	)

	twigResult, err := provider.GetSignatureHelp(
		context.Background(),
		adminSignatureRequest(
			childTemplate, childTemplateSource, "'b'",
		),
	)
	require.NoError(t, err)
	requireAdminSignature(
		t,
		twigResult,
		"save(id: string, options?: { force: boolean, tags: string[] }): Promise<void>",
		1,
	)
	assert.Contains(t, twigResult.Signatures[0].Documentation.Value, "sw-child")
}

func TestAdminSignatureHelpForTypedMarkupMembersAndVueBuiltins(t *testing.T) {
	root := t.TempDir()
	idx, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	adminRoot := filepath.Join(
		root, "src/Administration/Resources/app/administration/src",
	)
	definitionPath := filepath.Join(adminRoot, "component/sw-card/index.ts")
	templatePath := filepath.Join(filepath.Dir(definitionPath), "sw-card.html.twig")
	definitionSource := `import template from './sw-card.html.twig';
interface Handler {
    run: (value: string, retries?: number) => boolean;
}
Component.register('sw-card', {
    template,
    props: { handler: { type: Object as PropType<Handler> } },
});`
	templateSource := `<button @click="handler.run('value', 2); $emit('saved', handler)">Run</button>`
	require.NoError(t, idx.Index(indexer.NewParsedFile(
		definitionPath, []byte(definitionSource),
	)))
	require.NoError(t, idx.Index(indexer.NewParsedFile(
		templatePath, []byte(templateSource),
	)))
	component, err := idx.GetComponentByTemplatePath(templatePath)
	require.NoError(t, err)
	require.NotNil(t, component)
	handler, found := component.TemplateMember("handler")
	require.True(t, found)
	assert.Contains(t, handler.Type, "Handler")
	runOffset := uint32(strings.Index(templateSource, "run") + 1)
	resolvedRun, err := idx.ResolveTwigVueInstanceMember(
		lsp.NewTextDocument(
			uriutil.FileURI(templatePath), templateSource, 1,
		).SyntaxTree.Root,
		[]byte(templateSource), runOffset, templatePath,
	)
	require.NoError(t, err)
	require.NotNil(t, resolvedRun)
	require.True(t, resolvedRun.MemberFound)
	assert.Equal(
		t, "(value: string, retries?: number) => boolean",
		resolvedRun.Member.Type,
	)
	provider := NewAdminSignatureProvider(idx)

	nested, err := provider.GetSignatureHelp(
		context.Background(),
		adminSignatureRequest(templatePath, templateSource, " 2"),
	)
	require.NoError(t, err)
	requireAdminSignature(
		t, nested, "run(value: string, retries?: number): boolean", 1,
	)

	builtin, err := provider.GetSignatureHelp(
		context.Background(),
		adminSignatureRequest(templatePath, templateSource, "handler)"),
	)
	require.NoError(t, err)
	requireAdminSignature(
		t,
		builtin,
		"$emit(event: string, ...args: unknown[]): void",
		1,
	)
}

func TestAdminSignatureHelpForDynamicScopedSlotOverloads(t *testing.T) {
	root := t.TempDir()
	idx, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	adminRoot := filepath.Join(
		root, "src/Administration/Resources/app/administration/src",
	)
	for _, component := range []admin.VueComponent{
		{
			Name: "sw-card-a", FilePath: filepath.Join(adminRoot, "a/index.ts"),
			Slots: []admin.VueComponentSlot{{
				Name: "row", Members: []admin.VueComponentSlotMember{{
					Name: "selectItem", Type: "(value: string) => boolean",
				}},
			}},
		},
		{
			Name: "sw-card-b", FilePath: filepath.Join(adminRoot, "b/index.ts"),
			Slots: []admin.VueComponentSlot{{
				Name: "row", Members: []admin.VueComponentSlotMember{{
					Name: "selectItem", Type: "(value: number) => boolean",
				}},
			}},
		},
	} {
		require.NoError(t, idx.SaveComponent(component))
	}
	path := filepath.Join(adminRoot, "consumer.html.twig")
	source := `<component :is="active ? 'sw-card-a' : 'sw-card-b'"><template #row="props">{{ props.selectItem('value') }}</template></component>`
	result, err := NewAdminSignatureProvider(idx).GetSignatureHelp(
		context.Background(), adminSignatureRequest(path, source, "'value'"),
	)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, result.Signatures, 2)
	labels := []string{result.Signatures[0].Label, result.Signatures[1].Label}
	assert.ElementsMatch(t, []string{
		"selectItem(value: number): boolean",
		"selectItem(value: string): boolean",
	}, labels)
	assert.Contains(t, result.Signatures[0].Documentation.Value, "sw-card-")
	assert.Contains(t, result.Signatures[1].Documentation.Value, "sw-card-")
}

func TestAdminSignatureHelpForPiniaActions(t *testing.T) {
	root := t.TempDir()
	idx, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	path := filepath.Join(
		root,
		"src/Administration/Resources/app/administration/src/store/session.ts",
	)
	source := `Shopware.Store.register('session', {
    actions: {
        login(user: string, remember?: boolean): Promise<boolean> {
            return Promise.resolve(true);
        },
    },
});
Shopware.Store.get('session').login('admin', true);`
	require.NoError(t, idx.Index(indexer.NewParsedFile(path, []byte(source))))

	result, err := NewAdminSignatureProvider(idx).GetSignatureHelp(
		context.Background(),
		adminSignatureRequest(path, source, "true);"),
	)
	require.NoError(t, err)
	requireAdminSignature(
		t,
		result,
		"login(user: string, remember?: boolean): Promise<boolean>",
		1,
	)
	assert.Contains(t, result.Signatures[0].Documentation.Value, "session")
}

func TestAdminSignatureHelpForShopwareUtils(t *testing.T) {
	root := t.TempDir()
	idx, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	adminRoot := filepath.Join(
		root, "src/Administration/Resources/app/administration/src",
	)
	utilPath := filepath.Join(adminRoot, "core/service/util.service.ts")
	require.NoError(t, idx.Index(indexer.NewParsedFile(
		utilPath,
		[]byte(`export const format = { date };
export default { format };
function date(value: string, options?: { locale: string }): string { return value; }`),
	)))
	consumerPath := filepath.Join(adminRoot, "module/example/index.ts")
	provider := NewAdminSignatureProvider(idx)
	for _, source := range []string{
		`Shopware.Utils.format.date('2026-01-01', { locale: 'de-DE' });`,
		`const format = Shopware.Utils.format; format.date('2026-01-01', { locale: 'de-DE' });`,
		`const { date: formatDate } = Shopware.Utils.format; formatDate('2026-01-01', { locale: 'de-DE' });`,
	} {
		result, signatureErr := provider.GetSignatureHelp(
			context.Background(),
			adminSignatureRequest(consumerPath, source, "locale: 'de-DE'"),
		)
		require.NoError(t, signatureErr)
		requireAdminSignature(
			t, result,
			"date(value: string, options?: { locale: string }): string", 1,
		)
		assert.Contains(
			t, result.Signatures[0].Documentation.Value,
			"Shopware.Utils.format.date",
		)
	}
}

func TestAdminSignatureHelpSpecializesShopwareEventBusPayloads(t *testing.T) {
	root := t.TempDir()
	idx, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	adminRoot := filepath.Join(
		root, "src/Administration/Resources/app/administration/src",
	)
	for path, source := range map[string]string{
		filepath.Join(adminRoot, "core/service/util.service.ts"): `
import EventBus from './utils/eventBus.utils';
export default { EventBus };`,
		filepath.Join(adminRoot, "core/service/utils/eventBus.utils.ts"): `
interface Events extends Record<string | symbol, unknown> {
    save: string;
    done: undefined;
}
const emitter = mitt<Events>();
export default emitter;`,
	} {
		require.NoError(t, idx.Index(indexer.NewParsedFile(path, []byte(source))))
	}
	consumerPath := filepath.Join(adminRoot, "module/example/index.ts")
	provider := NewAdminSignatureProvider(idx)
	for _, test := range []struct {
		name, source, needle, label string
		active                      int
	}{
		{
			"emit payload", `Shopware.Utils.EventBus.emit('save', 'id');`, "'id'",
			`emit(event: "save", payload: string): void`, 1,
		},
		{
			"on handler alias", `const { EventBus } = Shopware.Utils;
EventBus.on('save', handler);`, "handler",
			`on(event: "save", handler: (payload: string) => void): void`, 1,
		},
		{
			"off optional handler", `const bus = Shopware.Utils.EventBus;
bus.off('save', handler);`, "handler",
			`off(event: "save", handler?: (payload: string) => void): void`, 1,
		},
		{
			"undefined payload is optional",
			`Shopware.Utils.EventBus.emit('done');`, "done",
			`emit(event: "done", payload?: undefined): void`, 0,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			result, signatureErr := provider.GetSignatureHelp(
				context.Background(),
				adminSignatureRequest(
					consumerPath, test.source, test.needle,
				),
			)
			require.NoError(t, signatureErr)
			requireAdminSignature(t, result, test.label, test.active)
			assert.Contains(
				t, result.Signatures[0].Documentation.Value,
				"Shopware EventBus event",
			)
		})
	}
}

func TestAdminSignatureHelpForFilterBindings(t *testing.T) {
	root := t.TempDir()
	idx, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	adminRoot := filepath.Join(
		root, "src/Administration/Resources/app/administration/src",
	)
	registrationPath := filepath.Join(adminRoot, "app/filter/currency.ts")
	require.NoError(t, idx.Index(indexer.NewParsedFile(
		registrationPath,
		[]byte(`Shopware.Filter.register('currency', (value: number, code: string): string => String(value));`),
	)))
	provider := NewAdminSignatureProvider(idx)
	for _, source := range []string{
		`const currencyFilter = Shopware.Filter.getByName('currency'); currencyFilter(42, 'EUR');`,
		`Shopware.Filter.getByName('currency')(42, 'EUR');`,
	} {
		result, signatureErr := provider.GetSignatureHelp(
			context.Background(),
			adminSignatureRequest(
				filepath.Join(adminRoot, "consumer.ts"), source, "'EUR'",
			),
		)
		require.NoError(t, signatureErr)
		requireAdminSignature(
			t, result, "currency(value: number, code: string): string", 1,
		)
	}
}

func requireAdminSignature(
	t *testing.T,
	result *protocol.SignatureHelp,
	label string,
	active int,
) {
	t.Helper()
	require.NotNil(t, result)
	require.Len(t, result.Signatures, 1)
	assert.Equal(t, label, result.Signatures[0].Label)
	assert.Equal(t, active, result.ActiveParameter)
	assert.Equal(t, active, result.Signatures[0].ActiveParameter)
	require.NotNil(t, result.Signatures[0].Documentation)
}

func adminSignatureRequest(
	path,
	source,
	needle string,
) *lsp.SignatureHelpRequest {
	document := lsp.NewTextDocument(uriutil.FileURI(path), source, 1)
	offset := uint32(strings.LastIndex(source, needle) + len(needle)/2)
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.SignatureHelpParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	return &lsp.SignatureHelpRequest{
		SignatureHelpParams: params,
		SyntaxContext: lsp.SyntaxContext{
			Document: document, DocumentContent: document.Text,
			DocumentTree: document.SyntaxTree, LineIndex: document.LineIndex,
			Root: document.SyntaxTree.Root,
			Node: document.SyntaxTree.Root.NodeAtOffset(offset),
		},
	}
}
