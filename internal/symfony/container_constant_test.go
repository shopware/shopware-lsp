package symfony

import (
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	xmlparser "github.com/shopware/shopware-lsp/internal/parser/xml"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestYAMLContainerConstantReferences(t *testing.T) {
	source := []byte(`parameters:
  modern: !php/const App\Mode::ACTIVE
  legacy: !php/const:App\Mode::LEGACY
  quoted_name: !php/const 'App\Mode::QUOTED'
  global: !php/const APP_LIMIT
  string: "!php/const:App\Mode::IGNORED"
  comment: value # !php/const App\Mode::IGNORED
`)
	references := YAMLContainerConstantReferences(source)
	require.Len(t, references, 4)
	assert.Equal(
		t,
		[]string{
			"App\\Mode::ACTIVE",
			"App\\Mode::LEGACY",
			"App\\Mode::QUOTED",
			"APP_LIMIT",
		},
		containerConstantNames(references),
	)
	for _, reference := range references {
		assert.Equal(
			t,
			reference.Name,
			string(source[reference.Range.Start:reference.Range.End]),
		)
	}
}

func TestXMLContainerConstantReferences(t *testing.T) {
	source := `<container><services><service id="app">
<argument type="constant">
  App\Mode::ACTIVE
</argument>
<argument type="string">App\Mode::IGNORED</argument>
</service></services></container>`
	document := xmlparser.Parse(source)
	references := XMLContainerConstantReferences(document.Tree.Root)
	require.Len(t, references, 1)
	assert.Equal(t, "App\\Mode::ACTIVE", references[0].Name)
	assert.Equal(
		t,
		references[0].Name,
		source[references[0].Range.Start:references[0].Range.End],
	)
}

func TestResolveContainerConstant(t *testing.T) {
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	source := []byte(`<?php
namespace App;
const APP_LIMIT = 10;
class BaseMode { public const INHERITED = 1; }
enum Mode {
    public const ACTIVE = 'active';
    case LEGACY;
}
class ChildMode extends BaseMode {}
`)
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		"/project/src/Mode.php",
		source,
	)))
	assert.Equal(
		t,
		semantic.GlobalConstantSymbol,
		requireConstant(t, ResolveContainerConstant(
			phpIndex,
			"App\\APP_LIMIT",
		)).Kind,
	)
	assert.Equal(
		t,
		semantic.ClassConstantSymbol,
		requireConstant(t, ResolveContainerConstant(
			phpIndex,
			"App\\Mode::ACTIVE",
		)).Kind,
	)
	assert.Equal(
		t,
		semantic.EnumCaseSymbol,
		requireConstant(t, ResolveContainerConstant(
			phpIndex,
			"App\\Mode::LEGACY",
		)).Kind,
	)
	assert.Equal(
		t,
		"INHERITED",
		requireConstant(t, ResolveContainerConstant(
			phpIndex,
			"App\\ChildMode::INHERITED",
		)).Name,
	)
	assert.Empty(t, ResolveContainerConstant(
		phpIndex,
		"App\\Mode::MISSING",
	))
}

func containerConstantNames(
	references []ContainerConstantReference,
) []string {
	result := make([]string, 0, len(references))
	for _, reference := range references {
		result = append(result, reference.Name)
	}
	return result
}

func requireConstant(
	t *testing.T,
	symbols []semantic.Symbol,
) semantic.Symbol {
	t.Helper()
	require.NotEmpty(t, symbols)
	return symbols[0]
}
