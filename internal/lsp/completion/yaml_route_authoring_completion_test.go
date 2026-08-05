package completion

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/symfony"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestYAMLRouteOptionCompletionBlockAndFlowMappings(t *testing.T) {
	provider := NewYAMLRouteAuthoringCompletionProvider(nil)
	for _, fixture := range []struct {
		name       string
		path       string
		source     string
		label      string
		replaced   string
		newText    string
		notPresent string
		deprecated bool
	}{
		{
			name: "modern block partial key",
			path: "/project/config/routes.yaml",
			source: `catalog:
  path: /catalog
  meth<caret>
`,
			label:      "methods",
			replaced:   "meth",
			newText:    "methods: ",
			notPresent: "path",
		},
		{
			name: "legacy routing filename",
			path: "/project/app/config/routing.yml",
			source: `catalog:
  pat<caret>
`,
			label:      "pattern",
			replaced:   "pat",
			newText:    "pattern: ",
			deprecated: true,
		},
		{
			name: "empty key under config routes directory",
			path: "/project/config/routes/catalog.yaml",
			source: `catalog:
  <caret>
`,
			label:    "controller",
			replaced: "",
			newText:  "controller: ",
		},
		{
			name: "complete quoted key",
			path: "/project/config/routes.yaml",
			source: `catalog:
  'hos<caret>t':
`,
			label:    "host",
			replaced: "'host'",
			newText:  "'host'",
		},
		{
			name: "flow mapping after path placeholder",
			path: "/project/config/routes.yaml",
			source: `catalog: { path: /catalog/{id}, req<caret> }
`,
			label:      "requirements",
			replaced:   "req",
			newText:    "requirements: ",
			notPresent: "path",
		},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			source, offset := completionCaret(t, fixture.source)
			document, request := bundleResourceCompletionRequest(
				t,
				fixture.path,
				source,
				offset,
			)
			items := provider.GetCompletions(context.Background(), request)
			item := requireCompletion(t, items, fixture.label)
			assert.Equal(t, int(protocol.PropertyCompletion), item.Kind)
			assert.Equal(t, fixture.deprecated, item.Deprecated)
			edit, ok := item.TextEdit.(protocol.TextEdit)
			require.True(t, ok)
			assert.Equal(t, fixture.newText, edit.NewText)
			assert.Equal(
				t,
				fixture.replaced,
				completionRangeText(document, edit.Range),
			)
			if fixture.notPresent != "" {
				assert.NotContains(
					t,
					completionLabels(items),
					fixture.notPresent,
				)
			}
		})
	}
}

func TestYAMLRouteOptionCompletionRejectsUnrelatedAndNestedMappings(
	t *testing.T,
) {
	provider := NewYAMLRouteAuthoringCompletionProvider(nil)
	for _, fixture := range []struct {
		path   string
		source string
	}{
		{
			path: "/project/config/services.yaml",
			source: `catalog:
  pat<caret>
`,
		},
		{
			path: "/project/config/routes.yaml",
			source: `catalog:
  defaults:
    pat<caret>
`,
		},
		{
			path: "/project/config/routes.yaml",
			source: `catalog:
  requirements:
    i<caret>
`,
		},
		{
			path: "/project/config/routes.yaml",
			source: `top<caret>
`,
		},
	} {
		source, offset := completionCaret(t, fixture.source)
		_, request := bundleResourceCompletionRequest(
			t,
			fixture.path,
			source,
			offset,
		)
		assert.Empty(t, provider.GetCompletions(
			context.Background(),
			request,
		))
	}
}

func TestYAMLRouteRequirementCompletionUsesPathPlaceholders(t *testing.T) {
	provider := NewYAMLRouteAuthoringCompletionProvider(nil)
	for _, fixture := range []struct {
		name       string
		source     string
		label      string
		replaced   string
		newText    string
		notPresent string
	}{
		{
			name: "unquoted partial",
			source: `catalog:
  path: /catalog/{id}/{slug}
  requirements:
    i<caret>
`,
			label:    "id",
			replaced: "i",
			newText:  "id: ",
		},
		{
			name: "quoted partial legacy pattern",
			source: `catalog:
  pattern: /catalog/{name}
  requirements:
    'n<caret>'
`,
			label:    "name",
			replaced: "'n'",
			newText:  "'name': ",
		},
		{
			name: "inline requirement and duplicate suppression",
			source: `catalog:
  path: '/catalog/{id<\d+>}/{slug}'
  requirements:
    id: '\d+'
    sl<caret>
`,
			label:      "slug",
			replaced:   "sl",
			newText:    "slug: ",
			notPresent: "id",
		},
		{
			name: "flow mapping",
			source: `catalog: { path: '/catalog/{id}/{slug}', requirements: { id: '\d+', sl<caret> } }
`,
			label:      "slug",
			replaced:   "sl",
			newText:    "slug: ",
			notPresent: "id",
		},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			source, offset := completionCaret(t, fixture.source)
			document, request := bundleResourceCompletionRequest(
				t,
				filepath.Join(t.TempDir(), "config", "routes.yaml"),
				source,
				offset,
			)
			items := provider.GetCompletions(context.Background(), request)
			item := requireCompletion(t, items, fixture.label)
			edit, ok := item.TextEdit.(protocol.TextEdit)
			require.True(t, ok)
			assert.Equal(t, fixture.newText, edit.NewText)
			assert.Equal(
				t,
				fixture.replaced,
				completionRangeText(document, edit.Range),
			)
			if fixture.notPresent != "" {
				assert.NotContains(
					t,
					completionLabels(items),
					fixture.notPresent,
				)
			}
		})
	}
}

func TestYAMLRouteRequirementCompletionRejectsMissingPathAndWrongLevel(
	t *testing.T,
) {
	provider := NewYAMLRouteAuthoringCompletionProvider(nil)
	for _, sourceWithCaret := range []string{
		`catalog:
  requirements:
    i<caret>
`,
		`catalog:
  path: /catalog/{id}
  defaults:
    i<caret>
`,
	} {
		source, offset := completionCaret(t, sourceWithCaret)
		_, request := bundleResourceCompletionRequest(
			t,
			"/project/config/routes.yaml",
			source,
			offset,
		)
		assert.Empty(t, provider.GetCompletions(
			context.Background(),
			request,
		))
	}
}

func TestYAMLRoutePathCompletionUsesIndexedRoutesWithExactEdits(t *testing.T) {
	routeIndex, err := symfony.NewRouteIndexer(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, routeIndex.Close()) })
	require.NoError(t, routeIndex.Index(indexer.NewParsedFile(
		"/project/config/routes.yaml",
		[]byte(`product.show:
  path: /products/{id}
product.duplicate:
  path: /products/{id}
catalog:
  path: /catalog
_internal:
  path: /_internal
`),
	)))
	provider := NewYAMLRouteAuthoringCompletionProvider(routeIndex)
	for _, fixture := range []struct {
		name     string
		source   string
		replaced string
	}{
		{
			name: "empty block value",
			source: `draft:
  path: <caret>
`,
			replaced: "",
		},
		{
			name: "unquoted partial block value",
			source: `draft:
  path: /pro<caret>
`,
			replaced: "/pro",
		},
		{
			name: "quoted partial block value",
			source: `draft:
  path: '/pro<caret>'
`,
			replaced: "/pro",
		},
		{
			name: "flow value",
			source: `draft: { path: /pro<caret>, methods: [GET] }
`,
			replaced: "/pro",
		},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			source, offset := completionCaret(t, fixture.source)
			document, request := bundleResourceCompletionRequest(
				t,
				"/project/config/routes.yaml",
				source,
				offset,
			)
			items := provider.GetCompletions(context.Background(), request)
			item := requireCompletion(t, items, "/products/{id}")
			assert.Equal(t, int(protocol.ReferenceCompletion), item.Kind)
			assert.Equal(t, "product.duplicate", item.Detail)
			edit, ok := item.TextEdit.(protocol.TextEdit)
			require.True(t, ok)
			assert.Equal(t, "/products/{id}", edit.NewText)
			assert.Equal(
				t,
				fixture.replaced,
				completionRangeText(document, edit.Range),
			)
			assert.NotContains(t, completionLabels(items), "/_internal")
			assert.Equal(
				t,
				1,
				completionLabelCount(items, "/products/{id}"),
			)
		})
	}
}

func TestYAMLRoutePathCompletionRejectsNestedResourcePaths(t *testing.T) {
	routeIndex, err := symfony.NewRouteIndexer(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, routeIndex.Close()) })
	provider := NewYAMLRouteAuthoringCompletionProvider(routeIndex)
	for _, sourceWithCaret := range []string{
		`controllers:
  resource:
    path: <caret>
`,
		`controllers: { resource: { path: <caret> } }
`,
	} {
		source, offset := completionCaret(t, sourceWithCaret)
		_, request := bundleResourceCompletionRequest(
			t,
			"/project/config/routes.yaml",
			source,
			offset,
		)
		assert.Empty(t, provider.GetCompletions(
			context.Background(),
			request,
		))
	}
}

func completionLabelCount(
	items []protocol.CompletionItem,
	label string,
) int {
	count := 0
	for _, item := range items {
		if item.Label == label {
			count++
		}
	}
	return count
}
