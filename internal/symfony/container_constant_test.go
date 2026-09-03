package symfony

import (
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	xmlparser "github.com/shopware/shopware-lsp/internal/parser/xml"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/php/project"
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
	case: !php/enum App\State::ACTIVE
	case_value: !php/enum App\State::ACTIVE->value
	quoted_case_value: !php/enum 'App\State::ACTIVE->value'
	all_cases: !php/enum App\State
  string: "!php/const:App\Mode::IGNORED"
	enum_string: "!php/enum App\State::IGNORED"
  comment: value # !php/const App\Mode::IGNORED
`)
	references := YAMLContainerConstantReferences(source)
	require.Len(t, references, 8)
	assert.Equal(
		t,
		[]string{
			"App\\Mode::ACTIVE",
			"App\\Mode::LEGACY",
			"App\\Mode::QUOTED",
			"APP_LIMIT",
			"App\\State::ACTIVE",
			"App\\State::ACTIVE",
			"App\\State::ACTIVE",
			"App\\State",
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
	assert.Equal(t, ContainerValueConstant, references[3].Kind)
	assert.Equal(t, ContainerValueEnum, references[4].Kind)
	assert.Equal(t, ContainerValueEnum, references[5].Kind)
	assert.Equal(t, ContainerValueEnum, references[6].Kind)
	assert.Equal(t, ContainerValueEnum, references[7].Kind)
}

func TestYAMLContainerEnumVersionSupport(t *testing.T) {
	model := func(version string) *project.Model {
		return &project.Model{Dependencies: []project.Package{{
			Name: "symfony/yaml", Version: version,
		}}}
	}
	assert.True(t, SupportsYAMLContainerEnum(nil))
	assert.False(t, SupportsYAMLContainerEnum(model("v6.1.12")))
	assert.True(t, SupportsYAMLContainerEnum(model("v6.2.0")))
	assert.False(t, SupportsYAMLContainerEnumClass(model("v7.0.9")))
	assert.True(t, SupportsYAMLContainerEnumClass(model("v7.1.0")))
}

func TestResolveContainerEnum(t *testing.T) {
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		"/project/src/Mode.php",
		[]byte(`<?php
namespace App;
enum Mode {
    public const ALIAS = self::ACTIVE;
    case ACTIVE;
}
class NotAnEnum { public const ACTIVE = 'active'; }
`),
	)))
	resolved := ResolveContainerValue(phpIndex, ContainerConstantReference{
		Name: "App\\Mode::ACTIVE", Kind: ContainerValueEnum,
	})
	assert.Equal(t, semantic.EnumCaseSymbol, requireConstant(t, resolved).Kind)
	resolved = ResolveContainerValue(phpIndex, ContainerConstantReference{
		Name: "App\\Mode", Kind: ContainerValueEnum,
	})
	assert.Equal(t, semantic.EnumSymbol, requireConstant(t, resolved).Kind)
	assert.Empty(t, ResolveContainerValue(phpIndex, ContainerConstantReference{
		Name: "App\\Mode::ALIAS", Kind: ContainerValueEnum,
	}))
	assert.Empty(t, ResolveContainerValue(phpIndex, ContainerConstantReference{
		Name: "App\\NotAnEnum", Kind: ContainerValueEnum,
	}))
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
