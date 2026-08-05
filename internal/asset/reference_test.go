package asset

import (
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReferencesExtractTwigAssetsEncoreAndPHPPackages(t *testing.T) {
	tests := []struct {
		path   string
		source string
		names  map[ReferenceKind][]string
	}{
		{
			path: "/project/templates/page.html.twig",
			source: `{{ asset('build/app.css') }}
{{ asset('administration/app.js', '@Administration') }}
{{ asset('ignored.css', {'package': 'nested-package'}) }}
{{ encore_entry_script_tags('app') }}
{{ vite_entry_link_tags('vite-app') }}
{{ vite_entry_script_tags('vite-admin') }}
{{ importmap('frontend') }}
{{ importmap(dynamic) }}
{{ encore_entry_link_tags(dynamic) }}
<link rel="stylesheet" href="/build/theme.css">
<script src="./build/runtime.js"></script>
<img src="images/logo.svg">
<img src="https://cdn.example.test/logo.svg">
<img src="{{ asset('images/dynamic.png') }}">`,
			names: map[ReferenceKind][]string{
				AssetReference: {
					"build/app.css",
					"administration/app.js",
					"ignored.css",
					"images/dynamic.png",
					"build/theme.css",
					"build/runtime.js",
					"images/logo.svg",
				},
				AssetPackageReference: {"@Administration"},
				EncoreEntryReference:  {"app"},
				ViteEntryReference:    {"vite-app", "vite-admin"},
				ImportmapReference:    {"frontend"},
			},
		},
		{
			path: "/project/src/Assets.php",
			source: `<?php
$packages->getUrl('build/app.css');
$packages->getVersion('build/app.js', 'theme');
$packages->getUrl(packageName: 'uploads', path: 'image.png');`,
			names: map[ReferenceKind][]string{
				AssetReference: {
					"build/app.css",
					"build/app.js",
					"image.png",
				},
				AssetPackageReference: {"theme", "uploads"},
			},
		},
	}
	for _, test := range tests {
		parsed := indexer.NewParsedFile(test.path, []byte(test.source))
		tree := parsed.SyntaxTree()
		require.NotNil(t, tree)
		references := References(test.path, tree.Root)
		actual := make(map[ReferenceKind][]string)
		var htmlTypes []HTMLAssetType
		for _, reference := range references {
			actual[reference.Kind] = append(
				actual[reference.Kind],
				reference.Name,
			)
			assert.NotZero(t, ReferenceRange(reference).Len())
			if reference.Kind == AssetReference &&
				reference.Name == "administration/app.js" {
				assert.Equal(t, "@Administration", reference.Package)
			}
			if reference.Kind == AssetPackageReference &&
				reference.Name == "@Administration" {
				assert.Equal(
					t,
					"administration/app.js",
					reference.AssetName,
				)
			}
			if reference.HTMLType != HTMLAssetNone {
				htmlTypes = append(htmlTypes, reference.HTMLType)
			}
		}
		assert.Equal(t, test.names, actual)
		if strings.HasSuffix(test.path, ".twig") {
			assert.Equal(t, []HTMLAssetType{
				HTMLAssetCSS,
				HTMLAssetJavaScript,
				HTMLAssetImage,
			}, htmlTypes)
		}
	}
}

func TestReferencesExtractOnlyStaticAsseticTagOperands(t *testing.T) {
	source := `{% stylesheets
    'css/app.css'
    '@MainBundle/Resources/public/css/theme.scss'
    'css/' ~ dynamic_name
    filter='cssrewrite'
%}
<link href="{{ asset_url }}">
{% endstylesheets %}
{% javascripts
    'js/app.js'
    '@named_formula'
%}
<script src="{{ asset_url }}"></script>
{% endjavascripts %}`
	parsed := indexer.NewParsedFile(
		"/project/templates/page.html.twig",
		[]byte(source),
	)
	references := References(
		parsed.Path,
		parsed.SyntaxTree().Root,
	)
	var assetic []Reference
	for _, reference := range references {
		if reference.Assetic {
			assetic = append(assetic, reference)
		}
	}
	require.Len(t, assetic, 4)
	assert.Equal(t, "css/app.css", assetic[0].Name)
	assert.Empty(t, assetic[0].Package)
	assert.Equal(t, HTMLAssetCSS, assetic[0].HTMLType)
	assert.Equal(t, "css/theme.scss", assetic[1].Name)
	assert.Equal(t, "@MainBundle", assetic[1].Package)
	assert.Equal(t, HTMLAssetCSS, assetic[1].HTMLType)
	assert.Equal(t, "js/app.js", assetic[2].Name)
	assert.Equal(t, HTMLAssetJavaScript, assetic[2].HTMLType)
	assert.Equal(t, "named_formula", assetic[3].Name)
	assert.Equal(t, AsseticNamedReference, assetic[3].Kind)
}
