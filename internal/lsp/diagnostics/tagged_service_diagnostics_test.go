package diagnostics

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTaggedServiceDiagnosticsForYAML(t *testing.T) {
	phpIndex := taggedServicePHPIndex(t)
	document := diagnosticsDocument(
		"file:///project/config/services.yaml",
		[]byte(`services:
  bad:
    class: App\BadExtension
    tags:
      - { name: twig.extension }
  good:
    class: App\GoodExtension
    tags: [twig.extension]
  legacy:
    class: App\LegacyExtension
    tags:
      - twig.extension
  unknown_tag:
    class: App\BadExtension
    tags: [app.custom]
  App\BadExtension:
    tags:
      - { name: twig.extension }
`),
	)
	result, err := NewTaggedServiceAnalyzer(phpIndex).
		Analyze(context.Background(), document)
	require.NoError(t, err)
	require.Len(t, result, 2)
	for _, diagnostic := range result {
		assert.Equal(t, taggedServiceTypeCode, diagnostic.ID)
		assert.Equal(
			t,
			protocol.DiagnosticSeverityWarning,
			diagnostic.Severity,
		)
		assert.Equal(
			t,
			"App\\BadExtension",
			problemRangeText(document, diagnostic.Range),
		)
		assert.Contains(t, diagnostic.Message, "twig.extension")
		assert.Contains(t, diagnostic.Message, "Twig_ExtensionInterface")
	}
}

func TestTaggedServiceDiagnosticsForXML(t *testing.T) {
	phpIndex := taggedServicePHPIndex(t)
	document := diagnosticsDocument(
		"file:///project/config/services.xml",
		[]byte(`<?xml version="1.0"?>
<container>
  <services>
    <service id="bad" class="App\BadForm">
      <tag name="form.type"/>
    </service>
    <service id="App\GoodForm">
      <tag name="form.type"/>
    </service>
    <service id="App\BadForm">
      <tag name="form.type"/>
    </service>
  </services>
</container>`),
	)
	result, err := NewTaggedServiceAnalyzer(phpIndex).
		Analyze(context.Background(), document)
	require.NoError(t, err)
	require.Len(t, result, 2)
	for _, diagnostic := range result {
		assert.Equal(
			t,
			"App\\BadForm",
			problemRangeText(document, diagnostic.Range),
		)
		assert.Contains(
			t,
			diagnostic.Message,
			"Symfony\\Component\\Form\\FormTypeInterface",
		)
	}
}

func TestTaggedServiceDiagnosticsSkipUnknownContracts(t *testing.T) {
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	source := []byte(`<?php
namespace App;
class Extension {}
`)
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		"/project/src/Extension.php",
		source,
	)))
	document := diagnosticsDocument(
		"file:///project/config/services.yaml",
		[]byte(`services:
  App\Extension:
    tags: [twig.extension]
`),
	)
	result, err := NewTaggedServiceAnalyzer(phpIndex).
		Analyze(context.Background(), document)
	require.NoError(t, err)
	assert.Empty(t, result)
}

func taggedServicePHPIndex(t *testing.T) *php.PHPIndex {
	t.Helper()
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	source := []byte(`<?php
namespace Twig\Extension {
    interface ExtensionInterface {}
}
namespace {
    interface Twig_ExtensionInterface {}
}
namespace Symfony\Component\Form {
    interface FormTypeInterface {}
}
namespace App {
    class BadExtension {}
    class GoodExtension implements \Twig\Extension\ExtensionInterface {}
    class LegacyExtension implements \Twig_ExtensionInterface {}
    class BadForm {}
    class GoodForm implements \Symfony\Component\Form\FormTypeInterface {}
}
`)
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		filepath.Join("/project", "src", "Types.php"),
		source,
	)))
	return phpIndex
}
