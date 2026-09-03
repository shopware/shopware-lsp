package completion

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/php/project"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestYAMLServiceOptionCompletionBlockAndFlowMappings(t *testing.T) {
	provider := NewYAMLServiceAuthoringCompletionProvider(nil)
	for _, fixture := range []struct {
		name       string
		source     string
		label      string
		replaced   string
		newText    string
		notPresent string
	}{
		{
			name: "block partial key",
			source: `services:
  app.consumer:
    class: App\Consumer
    aut<caret>
`,
			label:      "autowire",
			replaced:   "aut",
			newText:    "autowire: ",
			notPresent: "class",
		},
		{
			name: "block complete key",
			source: `services:
  app.consumer:
    clas<caret>s:
`,
			label:    "class",
			replaced: "class",
			newText:  "class",
		},
		{
			name: "empty block key",
			source: `services:
  app.consumer:
    <caret>
`,
			label:    "decoration_inner_name",
			replaced: "",
			newText:  "decoration_inner_name: ",
		},
		{
			name: "flow mapping",
			source: `services:
  app.consumer: { class: App\Consumer, deco<caret> }
`,
			label:      "decorates",
			replaced:   "deco",
			newText:    "decorates: ",
			notPresent: "class",
		},
		{
			name: "instanceof options",
			source: `services:
  _instanceof:
    App\Contract:
      auto<caret>
`,
			label:    "autoconfigure",
			replaced: "auto",
			newText:  "autoconfigure: ",
		},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			source, offset := completionCaret(t, fixture.source)
			document, request := bundleResourceCompletionRequest(
				t,
				filepath.Join(t.TempDir(), "config", "services.yaml"),
				source,
				offset,
			)
			items := provider.GetCompletions(context.Background(), request)
			item := requireCompletion(t, items, fixture.label)
			assert.Equal(t, int(protocol.PropertyCompletion), item.Kind)
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

func TestYAMLServiceOptionCompletionRejectsNestedMappings(t *testing.T) {
	provider := NewYAMLServiceAuthoringCompletionProvider(nil)
	for _, sourceWithCaret := range []string{
		`services:
  app.consumer:
    arguments:
      $log<caret>: '@logger'
`,
		`other:
  app.consumer:
    aut<caret>
`,
		`services:
  app.consumer: ~
  aut<caret>
`,
	} {
		source, offset := completionCaret(t, sourceWithCaret)
		_, request := bundleResourceCompletionRequest(
			t,
			filepath.Join(t.TempDir(), "config", "services.yaml"),
			source,
			offset,
		)
		assert.Empty(t, provider.GetCompletions(
			context.Background(),
			request,
		))
	}
}

func TestYAMLValueTagAndKeywordCompletion(t *testing.T) {
	provider := NewYAMLServiceAuthoringCompletionProvider(nil)
	source, offset := completionCaret(t, `services:
  app.consumer:
    arguments:
      - !tag<caret>
`)
	document, request := bundleResourceCompletionRequest(
		t,
		filepath.Join(t.TempDir(), "config", "services.yaml"),
		source,
		offset,
	)
	items := provider.GetCompletions(context.Background(), request)
	tagged := requireCompletion(t, items, "!tagged_iterator")
	edit, ok := tagged.TextEdit.(protocol.TextEdit)
	require.True(t, ok)
	assert.Equal(t, "!tagged_iterator ", edit.NewText)
	assert.Equal(t, "!tag", completionRangeText(document, edit.Range))
	assert.Contains(t, completionLabels(items), "!tagged_locator")
	assert.Contains(t, completionLabels(items), "!service_locator")
	assert.Contains(t, completionLabels(items), "!php/const")
	assert.Contains(t, completionLabels(items), "!php/enum")
	assert.NotContains(t, completionLabels(items), "!tagged")

	source, offset = completionCaret(t, "root:\n  enabled: tr<caret>\n")
	document, request = bundleResourceCompletionRequest(
		t,
		filepath.Join(t.TempDir(), "settings.yaml"),
		source,
		offset,
	)
	items = provider.GetCompletions(context.Background(), request)
	trueItem := requireCompletion(t, items, "true")
	edit, ok = trueItem.TextEdit.(protocol.TextEdit)
	require.True(t, ok)
	assert.Equal(t, "tr", completionRangeText(document, edit.Range))
	assert.NotContains(t, completionLabels(items), "!php/const")
}

func TestYAMLEnumTagRespectsInstalledSymfonyVersion(t *testing.T) {
	for _, fixture := range []struct {
		version string
		present bool
	}{
		{version: "v6.1.12", present: false},
		{version: "v6.2.0", present: true},
		{version: "v7.4.0", present: true},
	} {
		model := &project.Model{Dependencies: []project.Package{{
			Name: "symfony/yaml", Version: fixture.version,
		}}}
		provider := NewYAMLServiceAuthoringCompletionProvider(model)
		source, offset := completionCaret(t, "root:\n  value: !php/<caret>\n")
		_, request := bundleResourceCompletionRequest(
			t,
			filepath.Join(t.TempDir(), "config", "packages.yaml"),
			source,
			offset,
		)
		labels := completionLabels(
			provider.GetCompletions(context.Background(), request),
		)
		if fixture.present {
			assert.Contains(t, labels, "!php/enum", fixture.version)
		} else {
			assert.NotContains(t, labels, "!php/enum", fixture.version)
		}
	}
}

func TestYAMLServiceTagsRespectInstalledSymfonyVersion(t *testing.T) {
	for _, fixture := range []struct {
		version    string
		present    []string
		notPresent []string
	}{
		{
			version: "v4.3.9",
			present: []string{
				"!tagged",
				"!tagged_locator",
				"!service_locator",
			},
			notPresent: []string{"!tagged_iterator"},
		},
		{
			version: "v4.4.0",
			present: []string{
				"!tagged_iterator",
				"!tagged_locator",
				"!service_locator",
			},
			notPresent: []string{"!tagged"},
		},
	} {
		model := &project.Model{Dependencies: []project.Package{{
			Name:    "symfony/dependency-injection",
			Version: fixture.version,
		}}}
		provider := NewYAMLServiceAuthoringCompletionProvider(model)
		source, offset := completionCaret(t, `services:
  app.consumer:
    arguments: [<caret>]
`)
		_, request := bundleResourceCompletionRequest(
			t,
			filepath.Join(t.TempDir(), "config", "services.yaml"),
			source,
			offset,
		)
		labels := completionLabels(
			provider.GetCompletions(context.Background(), request),
		)
		for _, label := range fixture.present {
			assert.Contains(t, labels, label)
		}
		for _, label := range fixture.notPresent {
			assert.NotContains(t, labels, label)
		}
	}
}

func TestYAMLValueCompletionRejectsStringsAndValuesAfterTags(t *testing.T) {
	provider := NewYAMLServiceAuthoringCompletionProvider(nil)
	for _, sourceWithCaret := range []string{
		"root:\n  value: '<caret>'\n",
		"root:\n  value: !php/const <caret>\n",
		"root:\n  <caret>\n",
	} {
		source, offset := completionCaret(t, sourceWithCaret)
		_, request := bundleResourceCompletionRequest(
			t,
			filepath.Join(t.TempDir(), "config", "config.yaml"),
			source,
			offset,
		)
		assert.Empty(t, provider.GetCompletions(
			context.Background(),
			request,
		))
	}
}
