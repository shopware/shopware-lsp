package admin

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	javascriptparser "github.com/shopware/shopware-lsp/internal/parser/javascript"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestJavaScriptShopwareUtilsMemberTracksNestedReceivers(t *testing.T) {
	source := `
Shopware.Utils.createId();
Shopware.Utils.format.date('2026-01-01');
Shopware.Utils.EventBus.;
other.Utils.format.date();
`
	root := javascriptparser.Parse(source).Tree.Root
	tests := []struct {
		needle   string
		receiver []string
		member   string
		matched  bool
	}{
		{"createId", []string{}, "createId", true},
		{"date('2026", []string{"format"}, "date", true},
		{"EventBus.;", []string{"EventBus"}, "", true},
		{"other.Utils", nil, "", false},
	}
	for _, test := range tests {
		t.Run(test.needle, func(t *testing.T) {
			offset := strings.Index(source, test.needle)
			require.NotEqual(t, -1, offset)
			if test.needle == "EventBus.;" {
				offset += len("EventBus.") - 1
			} else {
				offset++
			}
			receiver, member, matched := JavaScriptShopwareUtilsMember(
				root.NodeAtOffset(uint32(offset)),
			)
			assert.Equal(t, test.matched, matched)
			assert.Equal(t, test.receiver, receiver)
			assert.Equal(t, test.member, member)
		})
	}
}

func TestJavaScriptShopwareUtilsMemberSupportsLexicalAliasesAndBindings(
	t *testing.T,
) {
	source := `
const utils = Shopware.Utils;
const format = Shopware.Utils.format;
const { types } = Shopware.Utils;
const { cloneDeep } = Shopware.Utils.object;
const { chunk: chunkArray } = Shopware.Utils.array;

function run() {
    utils.debug.warn('module alias');
    format.date('2026-01-01');
    types.isEmpty({});
    cloneDeep({});
    chunkArray([], 2);
    if (enabled) {
        const format = Shopware.Utils.string;
        format.camelCase('inner');
    }
    format.currency(10);
}

function mutableScope() {
    let format = Shopware.Utils.format;
    format.date('mutable');
}

function shadowedParameter(format) {
    format.date('parameter');
}
`
	root := javascriptparser.Parse(source).Tree.Root
	tests := []struct {
		needle     string
		occurrence int
		receiver   []string
		member     string
		matched    bool
	}{
		{"warn('module", 0, []string{"debug"}, "warn", true},
		{"date('2026", 0, []string{"format"}, "date", true},
		{"isEmpty", 0, []string{"types"}, "isEmpty", true},
		{"cloneDeep({", 0, []string{"object"}, "cloneDeep", true},
		{"chunkArray([],", 0, []string{"array"}, "chunk", true},
		{"camelCase", 0, []string{"string"}, "camelCase", true},
		{"currency", 0, []string{"format"}, "currency", true},
		{"date('mutable", 0, nil, "", false},
		{"date('parameter", 0, nil, "", false},
	}
	for _, test := range tests {
		t.Run(test.needle, func(t *testing.T) {
			offset := strings.Index(source, test.needle)
			require.NotEqual(t, -1, offset)
			receiver, member, matched := JavaScriptShopwareUtilsMember(
				root.NodeAtOffset(uint32(offset + 1)),
			)
			assert.Equal(t, test.matched, matched)
			assert.Equal(t, test.receiver, receiver)
			assert.Equal(t, test.member, member)
		})
	}

	renamedOffset := strings.Index(source, "chunkArray([],")
	require.NotEqual(t, -1, renamedOffset)
	renamedNode := root.NodeAtOffset(uint32(renamedOffset + 1))
	nameNode := JavaScriptShopwareUtilsMemberNameNode(renamedNode)
	require.NotNil(t, nameNode)
	assert.Equal(t, "chunkArray", strings.TrimSpace(nameNode.Text()))
}

func TestJavaScriptShopwareEventBusEventAtSupportsAliases(t *testing.T) {
	source := `
Shopware.Utils.EventBus.on('direct-event', handler);
const { EventBus } = Shopware.Utils;
EventBus.off('destructured-event', handler);
const bus = Shopware.Utils.EventBus;
bus.emit('aliased-event', payload);
bus.emit(('parenthesized-event'), payload);
export default Shopware.Component.wrapComponentConfig({
    methods: {
        save(): void {
            // eslint-disable-next-line @typescript-eslint/unbound-method
            EventBus.emit('nested-method-event', payload);
            // direct call comment
            Shopware.Utils.EventBus.emit('nested-direct-comment-event', payload);
        },
        shadowed(EventBus): void {
            EventBus.emit('shadowed-method-event', payload);
        },
    },
});
other.EventBus.emit('unrelated-event');
Shopware.Utils.EventBus.all.get('not-an-operation');
Shopware.Utils.EventBus.emit(enabled ? 'conditional-event' : 'fallback-event');
`
	source += "Shopware.Utils.EventBus.emit(`dynamic-${name}`);\n"
	root := javascriptparser.Parse(source).Tree.Root
	for _, test := range []struct {
		name      string
		operation string
		matched   bool
	}{
		{"direct-event", "on", true},
		{"destructured-event", "off", true},
		{"aliased-event", "emit", true},
		{"parenthesized-event", "emit", true},
		{"nested-method-event", "emit", true},
		{"nested-direct-comment-event", "emit", true},
		{"shadowed-method-event", "", false},
		{"unrelated-event", "", false},
		{"not-an-operation", "", false},
		{"conditional-event", "", false},
		{"dynamic-${name}", "", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			offset := strings.Index(source, test.name)
			require.NotEqual(t, -1, offset)
			operation, eventName, matched := JavaScriptShopwareEventBusEventAt(
				root.NodeAtOffset(uint32(offset + 1)),
			)
			assert.Equal(t, test.matched, matched)
			assert.Equal(t, test.operation, operation)
			if test.matched {
				assert.Equal(t, test.name, eventName)
			} else {
				assert.Empty(t, eventName)
			}
		})
	}
}

func TestResolveShopwareUtilsFollowsValueExportsAndDefaultImports(t *testing.T) {
	root := t.TempDir()
	idx, err := NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	adminRoot := filepath.Join(
		root, "src/Administration/Resources/app/administration/src",
	)
	utilPath := filepath.Join(adminRoot, "core/service/util.service.ts")
	debugPath := filepath.Join(adminRoot, "core/service/utils/debug.utils.ts")
	stringPath := filepath.Join(adminRoot, "core/service/utils/string.utils.ts")
	eventBusPath := filepath.Join(adminRoot, "core/service/utils/eventBus.utils.ts")
	for path, source := range map[string]string{
		utilPath: `
import { warn, error } from './utils/debug.utils';
import stringUtils from './utils/string.utils';
import EventBus from './utils/eventBus.utils';
export const debug = { warn, error };
export const string = { camelCase: stringUtils.camelCase, isUrl: stringUtils.isUrl };
export default { createId, debug, string, EventBus };
function createId(): string { return 'id'; }
`,
		debugPath: `
export default { warn, error };
export function warn(name: string, ...message: any[]): void {}
export function error(name: string, ...message: any[]): void {}
`,
		stringPath: `
import camelCase from 'lodash-es/camelCase';
export default { camelCase, isUrl };
function isUrl(value: string): boolean { return true; }
`,
		eventBusPath: `
interface Events extends Record<string | symbol, unknown> { save: string; }
const emitter = mitt<Events>();
export default emitter;
`,
	} {
		require.NoError(t, idx.Index(indexer.NewParsedFile(path, []byte(source))))
	}
	consumerPath := filepath.Join(adminRoot, "module/example/index.ts")

	rootShape, err := idx.ResolveShopwareUtils("", consumerPath)
	require.NoError(t, err)
	assert.True(t, rootShape.Complete)
	for _, name := range []string{"createId", "debug", "string", "EventBus"} {
		assert.Contains(t, vueMemberNames(rootShape.Members), name)
	}
	createID, found := twigVueMemberNamed(rootShape.Members, "createId")
	require.True(t, found)
	assert.Equal(t, "() => string", createID.Type)
	assert.Equal(t, utilPath, createID.DefinitionPath)
	assert.True(t, createID.DefinitionRange.Identifier)

	debugShape, err := idx.ResolveShopwareUtils("debug", consumerPath)
	require.NoError(t, err)
	assert.True(t, debugShape.Complete)
	warn, found := twigVueMemberNamed(debugShape.Members, "warn")
	require.True(t, found)
	assert.Equal(t, "(name: string, ...message: any[]) => void", warn.Type)
	assert.Equal(t, utilPath, warn.DefinitionPath)

	stringShape, err := idx.ResolveShopwareUtils("string", consumerPath)
	require.NoError(t, err)
	assert.True(t, stringShape.Complete)
	camelCase, found := twigVueMemberNamed(stringShape.Members, "camelCase")
	require.True(t, found)
	assert.Equal(t, "Function", camelCase.Type)
	isURL, found := twigVueMemberNamed(stringShape.Members, "isUrl")
	require.True(t, found)
	assert.Equal(t, "(value: string) => boolean", isURL.Type)

	eventBusShape, err := idx.ResolveShopwareUtils("EventBus", consumerPath)
	require.NoError(t, err)
	assert.False(t, eventBusShape.Complete)
	for _, name := range []string{"all", "emit", "off", "on"} {
		assert.Contains(t, vueMemberNames(eventBusShape.Members), name)
	}
	emit, found := twigVueMemberNamed(eventBusShape.Members, "emit")
	require.True(t, found)
	assert.Contains(t, emit.Type, "keyof Events")
	events, err := idx.ResolveShopwareEventBusEvents(consumerPath)
	require.NoError(t, err)
	assert.False(t, events.Complete)
	saveEvent, found := twigVueMemberNamed(events.Members, "save")
	require.True(t, found)
	assert.Equal(t, "string", saveEvent.Type)
	assert.Equal(t, eventBusPath, saveEvent.DefinitionPath)
	assert.True(t, saveEvent.DefinitionRange.Identifier)

	resolved, found, err := idx.resolveComponentExpressionType(
		VueComponent{}, "Shopware.Utils.createId()", consumerPath,
	)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "string", resolved.Type)
}
