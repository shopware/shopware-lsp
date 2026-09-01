package query

import (
	"slices"
	"strings"
	"testing"

	javascriptparser "github.com/shopware/shopware-lsp/internal/parser/javascript"
	"github.com/shopware/shopware-lsp/internal/parser/javascript/syntax"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	benchmarkQueryString string
	benchmarkQueryNode   *syntax.Node
	benchmarkQueryNodes  []*syntax.Node
	benchmarkQueryInt    int
)

func TestComponentCallQueries(t *testing.T) {
	root := javascriptparser.Parse(`Component.extend('child', 'parent', { props: { title: String } });`).Tree.Root
	calls := Calls(root, "Component.extend")
	require.Len(t, calls, 1)
	assert.Equal(t, "child", StringValue(StringArgument(calls[0], 0)))
	assert.Equal(t, "parent", StringValue(StringArgument(calls[0], 1)))

	object := ObjectArgument(calls[0], 2)
	require.NotNil(t, object)
	props := Property(object, "props")
	require.NotNil(t, props)
	require.NotNil(t, PropertyNameNode(props))
	assert.Equal(t, syntax.JsObject, PropertyValue(props).Kind())
	require.Len(t, Arguments(calls[0]), 3)

	iterator := IterateArguments(calls[0])
	require.Equal(t, 3, iterator.Len())
	for expected := 0; expected < 3; expected++ {
		require.True(t, iterator.Next())
		assert.Same(t, Argument(calls[0], expected), iterator.Node())
	}
	assert.False(t, iterator.Next())
}

func TestNodeIndexMatchesWholeTreeQueries(t *testing.T) {
	root := javascriptparser.Parse(`
const alias = Shopware.Utils;
alias.format.date(value);
Shopware.Component.register('sw-card', {});
`).Tree.Root
	index := NewNodeIndex(root)
	assert.Equal(t,
		Nodes(root, syntax.JsVariableDeclaration),
		index.Nodes(syntax.JsVariableDeclaration),
	)
	assert.Equal(t,
		Nodes(root, syntax.JsVariableDeclaration, syntax.JsCallExpression),
		index.Nodes(syntax.JsVariableDeclaration, syntax.JsCallExpression),
	)
	assert.Equal(t, Calls(root), index.Calls())
	assert.Equal(t,
		Calls(root, "Shopware.Component.register"),
		index.Calls("Shopware.Component.register"),
	)
	assert.Equal(t, Calls(root), slices.Collect(IterateCalls(root)))
	assert.Equal(t,
		Calls(root, "Shopware.Component.register"),
		slices.Collect(IterateCalls(root, "Shopware.Component.register")),
	)
	assert.Equal(t, Calls(root), slices.Collect(index.IterateCalls()))

	visited := 0
	for range IterateCalls(root) {
		visited++
		break
	}
	assert.Equal(t, 1, visited)
}

func BenchmarkCallsAndNodeIndex(b *testing.B) {
	root := javascriptparser.Parse(strings.Repeat(`
Shopware.Component.register('sw-card', {});
Shopware.Module.register('sw-module', {});
other();
`, 64)).Tree.Root
	b.Run("calls/all", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			benchmarkQueryNodes = Calls(root)
		}
	})
	b.Run("calls/named", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			benchmarkQueryNodes = Calls(root, "Shopware.Component.register")
		}
	})
	b.Run("iterate_calls/all", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			for call := range IterateCalls(root) {
				benchmarkQueryNode = call
			}
		}
	})
	b.Run("iterate_calls/named", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			for call := range IterateCalls(
				root,
				"Shopware.Component.register",
			) {
				benchmarkQueryNode = call
			}
		}
	})
	b.Run("node_index", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			index := NewNodeIndex(root)
			benchmarkQueryNodes = index.Nodes(syntax.JsCallExpression)
		}
	})
	b.Run("node_index/iterate_calls", func(b *testing.B) {
		index := NewNodeIndex(root)
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			for call := range index.IterateCalls() {
				benchmarkQueryNode = call
			}
		}
	})
}

func TestExportAndImportQueries(t *testing.T) {
	root := javascriptparser.Parse(`import template from './x.html.twig'; export default { template, emits: ['save'] };`).Tree.Root
	assert.Equal(t, "./x.html.twig", ImportPath(root, "template"))
	exports := ExportDefaults(root)
	require.Len(t, exports, 1)
	object := ExportDefaultExpression(exports[0])
	require.NotNil(t, object)
	assert.Equal(t, syntax.JsObject, object.Kind())
	assert.Equal(t, "save", StringValue(ArrayItems(PropertyValue(Property(object, "emits")))[0]))
}

func TestStringCursorContext(t *testing.T) {
	source := `Component.extend('child', 'parent')`
	root := javascriptparser.Parse(source).Tree.Root
	node := root.NodeAtOffset(uint32(len(source) - 4))
	require.NotNil(t, node)
	assert.Equal(t, "parent", StringValue(node))
	assert.Equal(t, 1, StringArgumentIndex(node))
	assert.Equal(t, "Component.extend", CallName(node))
}

func TestThisMemberContext(t *testing.T) {
	source := `this.repository.search()`
	root := javascriptparser.Parse(source).Tree.Root
	repository := root.NodeAtOffset(uint32(strings.Index(source, "repository") + 1))
	name, found := ThisMember(repository)
	require.True(t, found)
	assert.Equal(t, "repository", name)

	search := root.NodeAtOffset(uint32(strings.Index(source, "search") + 1))
	_, found = ThisMember(search)
	assert.False(t, found)

	incomplete := `this.`
	root = javascriptparser.Parse(incomplete).Tree.Root
	name, found = ThisMember(root.NodeAtOffset(uint32(len(incomplete) - 1)))
	require.True(t, found)
	assert.Empty(t, name)
}

func TestCallMethodNameForFluentCalls(t *testing.T) {
	source := `Application.addServiceProvider('one', factory)
        .addServiceProvider('two', factory);
Shopware.Service().register('three', factory);`
	root := javascriptparser.Parse(source).Tree.Root
	calls := Calls(root)
	var methods []string
	for _, call := range calls {
		methods = append(methods, CallMethodName(call))
	}
	assert.Contains(t, methods, "addServiceProvider")
	assert.Contains(t, methods, "register")
}

func TestCallNameIgnoresLeadingComments(t *testing.T) {
	source := `
// eslint-disable-next-line
Module.register('sw-product', {});
/* keep this call discoverable */
Shopware.Component.register('sw-card', {});`
	root := javascriptparser.Parse(source).Tree.Root
	require.Len(t, Calls(root, "Module.register"), 1)
	require.Len(t, Calls(root, "Shopware.Component.register"), 1)
}

func TestCallNamePreservesTriviaInsideComputedMemberLiterals(t *testing.T) {
	root := javascriptparser.Parse(
		`registry["a b"].handlers["https://example.test"]();`,
	).Tree.Root
	calls := Calls(root)
	require.Len(t, calls, 1)
	assert.Equal(
		t,
		`registry["a b"].handlers["https://example.test"]`,
		CallName(calls[0]),
	)
}

func TestPropertyNameIgnoresLeadingCommentsAndMethodModifiers(t *testing.T) {
	source := `({ methods: {
        // eslint-disable-next-line
        async validate(value: string): Promise<void> {},
    } })`
	root := javascriptparser.Parse(source).Tree.Root
	objects := Nodes(root, syntax.JsObject)
	require.GreaterOrEqual(t, len(objects), 2)
	methodsProperty := Property(objects[0], "methods")
	require.NotNil(t, methodsProperty)
	methods := Properties(PropertyValue(methodsProperty))
	require.Len(t, methods, 1)
	assert.Equal(t, "validate", PropertyName(methods[0]))
}

func TestStoreMemberContext(t *testing.T) {
	source := `Shopware.Store.get('session').currentUser.id`
	root := javascriptparser.Parse(source).Tree.Root
	currentUser := root.NodeAtOffset(uint32(strings.Index(source, "currentUser") + 1))
	store, member, found := StoreMember(currentUser)
	require.True(t, found)
	assert.Equal(t, "session", store)
	assert.Equal(t, "currentUser", member)

	id := root.NodeAtOffset(uint32(strings.LastIndex(source, "id") + 1))
	_, _, found = StoreMember(id)
	assert.False(t, found)

	incomplete := `Store.get('context').`
	root = javascriptparser.Parse(incomplete).Tree.Root
	store, member, found = StoreMember(
		root.NodeAtOffset(uint32(len(incomplete) - 1)),
	)
	require.True(t, found)
	assert.Equal(t, "context", store)
	assert.Empty(t, member)
}

func BenchmarkCallNameCompactExpression(b *testing.B) {
	root := javascriptparser.Parse(
		`Shopware.Component.register('sw-card', {});`,
	).Tree.Root
	call := Calls(root)[0]

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		benchmarkQueryString = CallName(call)
	}
}

func BenchmarkCallNameExpressionWithOuterTrivia(b *testing.B) {
	root := javascriptparser.Parse(
		"\n    Shopware.Component.register('sw-card', {});\n",
	).Tree.Root
	call := Calls(root)[0]
	require.Equal(b, "Shopware.Component.register", CallName(call))

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		benchmarkQueryString = CallName(call)
	}
}

func BenchmarkCallNameExpressionWithTrivia(b *testing.B) {
	root := javascriptparser.Parse(
		`Shopware /* keep */ . Component . register('sw-card', {});`,
	).Tree.Root
	call := Calls(root)[0]

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		benchmarkQueryString = CallName(call)
	}
}

func BenchmarkIdentifierText(b *testing.B) {
	root := javascriptparser.Parse(
		`Shopware.Component.register('sw-card', {});`,
	).Tree.Root
	identifier := MemberNameNode(CallCallee(Calls(root)[0]))
	require.NotNil(b, identifier)

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		benchmarkQueryString = IdentifierText(identifier)
	}
}

func BenchmarkArgumentExpression(b *testing.B) {
	root := javascriptparser.Parse(
		`Shopware.Component.register('sw-card', {}, options);`,
	).Tree.Root
	call := Calls(root)[0]

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		benchmarkQueryNode = ArgumentExpression(call, 2)
	}
}

func BenchmarkStringArgumentIndex(b *testing.B) {
	root := javascriptparser.Parse(
		`Shopware.Component.extend('child', 'parent', {});`,
	).Tree.Root
	literal := StringArgument(Calls(root)[0], 1)
	require.NotNil(b, literal)

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		benchmarkQueryInt = StringArgumentIndex(literal)
	}
}
