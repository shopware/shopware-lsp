package translation

import (
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIndexYAMLTranslationResource(t *testing.T) {
	idx := newTestIndex(t)
	require.NoError(t, idx.Index(indexer.NewParsedFile(
		"/project/translations/messages.en.yaml",
		[]byte(`checkout:
  cart:
    title: 'Your cart'
plain: Plain value
`),
	)))

	messages, err := idx.GetMessages("messages", "checkout.cart.title")
	require.NoError(t, err)
	require.Len(t, messages, 1)
	assert.Equal(t, "Your cart", messages[0].Text)
	assert.Equal(t, "en", messages[0].Locale)
	assert.Equal(t, 2, messages[0].Line)
	assert.Equal(t, "yaml", messages[0].Format)

	keys, err := idx.GetKeys("messages")
	require.NoError(t, err)
	assert.Equal(t, []string{"checkout.cart.title", "plain"}, keys)
}

func TestIndexXLIFFTranslationResources(t *testing.T) {
	idx := newTestIndex(t)
	require.NoError(t, idx.Index(indexer.NewParsedFile(
		"/project/translations/validators.de.xlf",
		[]byte(`<?xml version="1.0"?>
<xliff version="1.2"><file><body>
  <trans-unit id="1" resname="not.blank">
    <source>not.blank.fallback</source>
    <target>Dieser Wert sollte nicht leer sein.</target>
  </trans-unit>
</body></file></xliff>`),
	)))
	require.NoError(t, idx.Index(indexer.NewParsedFile(
		"/project/translations/messages.fr.xliff",
		[]byte(`<?xml version="1.0"?>
<xliff version="2.0"><file>
  <unit id="hello"><segment>
    <source>hello.world</source>
    <target>Bonjour</target>
  </segment></unit>
</file></xliff>`),
	)))

	validators, err := idx.GetMessages("validators", "not.blank")
	require.NoError(t, err)
	require.Len(t, validators, 1)
	assert.Equal(t, "Dieser Wert sollte nicht leer sein.", validators[0].Text)
	assert.Equal(t, "xlf", validators[0].Format)

	messages, err := idx.GetMessages("messages", "hello.world")
	require.NoError(t, err)
	require.Len(t, messages, 1)
	assert.Equal(t, "Bonjour", messages[0].Text)
}

func TestIndexPHPTranslationResource(t *testing.T) {
	idx := newTestIndex(t)
	require.NoError(t, idx.Index(indexer.NewParsedFile(
		"/project/translations/messages.en.php",
		[]byte(`<?php
return [
    'hello.world' => 'Hello world',
    'with.placeholder' => 'Hello %name%',
];
`),
	)))

	messages, err := idx.GetMessages("messages", "hello.world")
	require.NoError(t, err)
	require.Len(t, messages, 1)
	assert.Equal(t, "Hello world", messages[0].Text)
	assert.Equal(t, "php", messages[0].Format)
	assert.Equal(t, 2, messages[0].Line)
}

func TestIndexCompiledPHPMessageCatalogue(t *testing.T) {
	idx := newTestIndex(t)
	require.NoError(t, idx.Index(indexer.NewParsedFile(
		"/project/var/cache/dev/translations/catalogue.en.php",
		[]byte(`<?php
$catalogue = new \Symfony\Component\Translation\MessageCatalogue('en_GB', [
    'messages' => [
        'hello.world' => 'Hello world',
    ],
    'validators' => [
        'not.blank' => 'This value should not be blank.',
    ],
    'shop+intl-icu' => [
        'items' => '{count, plural, one {item} other {items}}',
    ],
]);
`),
	)))

	domains, err := idx.GetDomains()
	require.NoError(t, err)
	assert.Equal(t, []string{"messages", "shop", "validators"}, domains)

	messages, err := idx.GetMessages("messages", "hello.world")
	require.NoError(t, err)
	require.Len(t, messages, 1)
	assert.Equal(t, "en_GB", messages[0].Locale)

	icu, err := idx.GetMessages("shop", "items")
	require.NoError(t, err)
	require.Len(t, icu, 1)
	assert.Equal(t, "{count, plural, one {item} other {items}}", icu[0].Text)
}

func TestTranslationIndexReplacesAndRemovesFile(t *testing.T) {
	idx := newTestIndex(t)
	path := "/project/translations/messages.en.yaml"
	require.NoError(t, idx.Index(indexer.NewParsedFile(
		path,
		[]byte("first: First\n"),
	)))
	require.NoError(t, idx.Index(indexer.NewParsedFile(
		path,
		[]byte("second: Second\n"),
	)))

	first, err := idx.GetMessages("messages", "first")
	require.NoError(t, err)
	assert.Empty(t, first)
	second, err := idx.GetMessages("messages", "second")
	require.NoError(t, err)
	require.Len(t, second, 1)

	require.NoError(t, idx.RemovedFiles([]string{path}))
	second, err = idx.GetMessages("messages", "second")
	require.NoError(t, err)
	assert.Empty(t, second)
}

func TestCatalogueMetadata(t *testing.T) {
	tests := []struct {
		path     string
		domain   string
		locale   string
		compiled bool
		ok       bool
	}{
		{"/project/translations/messages.en.yaml", "messages", "en", false, true},
		{"/project/translations/admin+intl-icu.de_DE.xlf", "admin", "de_DE", false, true},
		{"/project/var/cache/dev/translations/catalogue.en.php", "catalogue", "en", true, true},
		{"/project/config/services.yaml", "", "", false, false},
	}
	for _, test := range tests {
		metadata, ok := catalogueMetadata(test.path)
		require.Equal(t, test.ok, ok, test.path)
		if !ok {
			continue
		}
		assert.Equal(t, test.domain, metadata.domain, test.path)
		assert.Equal(t, test.locale, metadata.locale, test.path)
		assert.Equal(t, test.compiled, metadata.compiled, test.path)
	}
}

func newTestIndex(t *testing.T) *Index {
	t.Helper()
	idx, err := NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	return idx
}
