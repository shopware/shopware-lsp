package twigcomponent

import (
	"strings"
	"testing"

	phpparser "github.com/shopware/shopware-lsp/internal/parser/php"
	twigparser "github.com/shopware/shopware-lsp/internal/parser/twig"
	yamlparser "github.com/shopware/shopware-lsp/internal/parser/yaml"
	"github.com/stretchr/testify/require"
)

func TestDeclarationsInPHP(t *testing.T) {
	source := `<?php
namespace App\Twig\Components;

use Symfony\UX\TwigComponent\Attribute\AsTwigComponent as Component;

#[Component(name: 'Shop:Card', template: 'ui/card.html.twig', exposePublicProps: false)]
final class Card {}
`
	tree := phpparser.Parse(source).Tree
	declarations := declarationsInPHP("/project/src/Card.php", tree.Root)
	require.Len(t, declarations, 1)
	require.Equal(t, "Shop:Card", declarations[0].Name)
	require.Equal(t, "App\\Twig\\Components\\Card", declarations[0].Class)
	require.Equal(t, "ui/card.html.twig", declarations[0].Template)
	require.False(t, declarations[0].ExposePublicProps)
	require.Equal(
		t,
		"Shop:Card",
		source[declarations[0].NameRange.Start:declarations[0].NameRange.End],
	)
	require.Equal(
		t,
		"Card",
		source[declarations[0].ClassRange.Start:declarations[0].ClassRange.End],
	)
}

func TestPHPPropsPreserveExposeMappingsAndLiveWritability(t *testing.T) {
	source := `<?php
namespace App\Twig\Components;

use Symfony\UX\LiveComponent\Attribute\AsLiveComponent;
use Symfony\UX\LiveComponent\Attribute\LiveProp;
use Symfony\UX\TwigComponent\Attribute\ExposeInTemplate;

#[AsLiveComponent]
final class Search {
    #[ExposeInTemplate(name: 'headline')]
    private string $title;

    #[ExposeInTemplate(getter: 'fetchIcon')]
    private string $icon;

    #[LiveProp(writable: true)]
    public string $query = '';

    #[ExposeInTemplate('state')]
    public function getStatus(): string {}

    private function fetchIcon(): string {}
}
`
	tree := phpparser.Parse(source).Tree
	declarations := declarationsInPHP("/project/src/Search.php", tree.Root)
	require.Len(t, declarations, 1)
	require.True(t, declarations[0].Live)

	props := propsInPHP("/project/src/Search.php", tree.Root)
	require.Len(t, props, 4)
	byName := make(map[string]Prop)
	for _, prop := range props {
		byName[prop.Name] = prop
		require.Equal(t, "App\\Twig\\Components\\Search", prop.Class)
	}
	require.Equal(t, "string", byName["headline"].Type)
	require.Equal(t, "string", byName["icon"].Type)
	require.Equal(t, "string", byName["state"].Type)
	require.True(t, byName["query"].Live)
	require.True(t, byName["query"].Writable)
	require.Equal(
		t,
		"$query",
		source[byName["query"].Range.Start:byName["query"].Range.End],
	)
}

func TestLiveListenerDeclarationsAndPHPEmissions(t *testing.T) {
	source := `<?php
namespace App\Twig\Components;

use Symfony\UX\LiveComponent\Attribute\LiveArg;
use Symfony\UX\LiveComponent\Attribute\LiveListener;

final class Cart {
    #[LiveListener('productAdded')]
    #[LiveListener(event: 'productRestored')]
    public function refresh(
        #[LiveArg('productId')] int $id,
        string $source = 'shop',
    ): void {}

    public function save(): void
    {
        $this->emit('productAdded', ['productId' => 42]);
        $this->emitUp(eventName: 'productRestored', data: ['source' => 'archive']);
        $this->emitSelf($dynamic);
        $this->emit("product$dynamic");
        $other->emit('not-a-live-event');
    }
}
`
	tree := phpparser.Parse(source).Tree
	listeners := LiveListenersInPHP("/project/src/Cart.php", tree.Root)
	require.Len(t, listeners, 2)
	require.Equal(t, "productAdded", listeners[0].Name)
	require.Equal(t, "productRestored", listeners[1].Name)
	require.Equal(t, "refresh", listeners[0].Method)
	require.Len(t, listeners[0].Parameters, 2)
	require.Equal(t, "productId", listeners[0].Parameters[0].Name)
	require.Equal(t, "id", listeners[0].Parameters[0].PHPName)
	require.Equal(
		t,
		"productAdded",
		source[listeners[0].Range.Start:listeners[0].Range.End],
	)

	references := LiveEventReferencesInPHP(
		"/project/src/Cart.php",
		tree.Root,
	)
	require.Len(t, references, 2)
	require.Equal(t, "productAdded", references[0].Name)
	require.Equal(t, LiveEventEmitReference, references[0].Kind)
	require.Equal(t, "productRestored", references[1].Name)
	require.Equal(t, LiveEventEmitUpReference, references[1].Kind)

	arguments := LiveEventArgumentReferencesInPHP(
		"/project/src/Cart.php",
		tree.Root,
	)
	require.Len(t, arguments, 2)
	require.Equal(t, "productId", arguments[0].Name)
	require.Equal(t, "source", arguments[1].Name)
}

func TestLiveEventReferencesInTwigRequireEmitAction(t *testing.T) {
	source := `<button
    data-action="click->live#emit"
    data-live-event-param="name(ProductList)|productAdded"
    data-live-product-id-param="42"
></button>
<button
    data-action="live#emitSelf"
    data-live-event-param="refreshed"
></button>
<button data-live-event-param="ignored"></button>
<button data-action="live#emit" data-live-event-param="{{ dynamic }}"></button>`
	tree := twigparser.Parse(source).Tree
	references := LiveEventReferencesInTwig(
		"/project/templates/Cart.html.twig",
		tree.Root,
	)
	require.Len(t, references, 2)
	require.Equal(t, "productAdded", references[0].Name)
	require.Equal(t, LiveEventAttributeReference, references[0].Kind)
	require.Equal(
		t,
		"productAdded",
		source[references[0].Range.Start:references[0].Range.End],
	)
	require.Equal(t, "refreshed", references[1].Name)
	require.Equal(t, LiveEventEmitSelfReference, references[1].Kind)

	arguments := LiveEventArgumentReferencesInTwig(
		"/project/templates/Cart.html.twig",
		tree.Root,
	)
	require.Len(t, arguments, 1)
	require.Equal(t, "productAdded", arguments[0].Event)
	require.Equal(t, "productId", arguments[0].Name)
}

func TestConfigurationInYAML(t *testing.T) {
	source := `twig_component:
  anonymous_template_directory: ui/components/
  defaults:
    App\Twig\Components\: components/
    App\Pizza\Components\:
      template_directory: components/pizza
      name_prefix: Pizza
when@test:
  twig_component:
    defaults:
      App\Test\Components\: test/components/
`
	tree := yamlparser.Parse(source).Tree
	namespaces, anonymous := configurationInYAML(
		"/project/config/packages/twig_component.yaml",
		tree.Root,
	)
	require.Equal(t, []string{"ui/components/"}, anonymous)
	require.Len(t, namespaces, 3)
	require.Equal(t, "App\\Twig\\Components", namespaces[0].ClassPrefix)
	require.Equal(t, "components/", namespaces[0].TemplateDirectory)
	require.Equal(t, "Pizza", namespaces[1].NamePrefix)
	require.Equal(t, "test/components/", namespaces[2].TemplateDirectory)
}

func TestConfigurationInPHPArrayForms(t *testing.T) {
	source := `<?php
return [
    'twig_component' => [
        'defaults' => [
            'App\\PhpShort\\Components\\' => 'php/components',
            'App\\PhpLong\\Components\\' => [
                'template_directory' => 'php/long',
                'name_prefix' => 'PhpLong',
            ],
            'App\\PhpDefaulted\\Components\\' => [
                'name_prefix' => '',
            ],
        ],
        'anonymous_template_directory' => 'php_components',
    ],
    'when@test' => [
        'twig_component' => [
            'defaults' => [
                'App\\PhpWhen\\Components\\' => 'php/when',
            ],
        ],
    ],
];`
	tree := phpparser.Parse(source).Tree
	namespaces, anonymous := configurationInPHP(
		"/project/config/packages/twig_component.php",
		tree.Root,
	)
	require.Equal(t, []string{"php_components/"}, anonymous)
	require.Len(t, namespaces, 4)
	byClass := make(map[string]Namespace)
	for _, namespace := range namespaces {
		byClass[namespace.ClassPrefix] = namespace
	}
	require.Equal(
		t,
		"php/components/",
		byClass["App\\PhpShort\\Components"].TemplateDirectory,
	)
	require.Equal(
		t,
		"php/long/",
		byClass["App\\PhpLong\\Components"].TemplateDirectory,
	)
	require.Equal(
		t,
		"PhpLong",
		byClass["App\\PhpLong\\Components"].NamePrefix,
	)
	require.Equal(
		t,
		"components/",
		byClass["App\\PhpDefaulted\\Components"].TemplateDirectory,
	)
	require.Equal(
		t,
		"php/when/",
		byClass["App\\PhpWhen\\Components"].TemplateDirectory,
	)
}

func TestConfigurationInPHPAppConfigForms(t *testing.T) {
	source := `<?php
namespace Symfony\Component\DependencyInjection\Loader\Configurator;

App::config([
    'twig_component' => [
        'defaults' => [
            'App\\Statement\\Components\\' => 'statement/components',
        ],
    ],
]);

return App::config([
    'when@test' => [
        'twig_component' => [
            'defaults' => [
                'App\\Returned\\Components\\' => 'returned/components',
            ],
        ],
    ],
]);`
	tree := phpparser.Parse(source).Tree
	namespaces, anonymous := configurationInPHP(
		"/project/config/packages/twig_component.php",
		tree.Root,
	)
	require.Empty(t, anonymous)
	require.Len(t, namespaces, 2)
	byClass := make(map[string]Namespace)
	for _, namespace := range namespaces {
		byClass[namespace.ClassPrefix] = namespace
	}
	require.Equal(
		t,
		"statement/components/",
		byClass["App\\Statement\\Components"].TemplateDirectory,
	)
	require.Equal(
		t,
		"returned/components/",
		byClass["App\\Returned\\Components"].TemplateDirectory,
	)
}

func TestConfigurationInPHPContainerExtensionClosure(t *testing.T) {
	source := `<?php
use Symfony\Component\DependencyInjection\Loader\Configurator\ContainerConfigurator;

return static function (ContainerConfigurator $container): void {
    $container->extension('twig_component', [
        'defaults' => [
            'App\\Shared\\Ui\\Component\\' => [
                'template_directory' => '@Shared',
                'name_prefix' => 'Shared',
            ],
        ],
        'anonymous_template_directory' => 'ux-components',
    ]);
};`
	tree := phpparser.Parse(source).Tree
	namespaces, anonymous := configurationInPHP(
		"/project/config/packages/twig_component.php",
		tree.Root,
	)
	require.Equal(t, []string{"ux-components/"}, anonymous)
	require.Len(t, namespaces, 1)
	require.Equal(
		t,
		"App\\Shared\\Ui\\Component",
		namespaces[0].ClassPrefix,
	)
	require.Equal(t, "@Shared/", namespaces[0].TemplateDirectory)
	require.Equal(t, "Shared", namespaces[0].NamePrefix)
}

func TestConfigurationInPHPIgnoresNestedAndUnrelatedConfigs(t *testing.T) {
	source := `<?php
return [
    'not_config' => static function ($container): void {
        $container->extension('twig_component', [
            'defaults' => [
                'App\\Nested\\Components\\' => 'nested/components',
            ],
        ]);
    },
    'also_not_config' => [
        'twig_component' => [
            'anonymous_template_directory' => 'nested',
        ],
    ],
];`
	tree := phpparser.Parse(source).Tree
	namespaces, anonymous := configurationInPHP(
		"/project/config/packages/twig_component.php",
		tree.Root,
	)
	require.Empty(t, namespaces)
	require.Empty(t, anonymous)
}

func TestUsagesAndPropsInTwig(t *testing.T) {
	source := `{# @prop variant 'primary'|'danger' Visual style #}
{% props icon, variant = 'primary' %}
{{ component('Alert', {icon: icon}) }}
{% component 'Modal' %}{% endcomponent %}
{% component Notice with {type: 'success'} %}{% endcomponent %}
<twig:Nav:Item :active="true" />`
	tree := twigparser.Parse(source).Tree
	usages := usagesInTwig("/project/templates/page.html.twig", tree.Root)
	require.Len(t, usages, 4)
	require.Equal(t, "Alert", usages[0].Name)
	require.Equal(t, FunctionUsage, usages[0].Kind)
	require.Equal(t, "Modal", usages[1].Name)
	require.Equal(t, BlockUsage, usages[1].Kind)
	require.Equal(t, "Notice", usages[2].Name)
	require.Equal(t, BlockUsage, usages[2].Kind)
	require.Equal(t, "Nav:Item", usages[3].Name)
	require.Equal(t, HTMLUsage, usages[3].Kind)
	for _, usage := range usages {
		require.Equal(
			t,
			usage.Name,
			source[usage.Range.Start:usage.Range.End],
		)
	}

	props := propsInTwig("/project/templates/components/Alert.html.twig", tree.Root)
	require.Len(t, props, 2)
	require.Equal(t, "icon", props[0].Name)
	require.Empty(t, props[0].DefaultValue)
	require.Equal(t, "variant", props[1].Name)
	require.Equal(t, `'primary'`, props[1].DefaultValue)
	require.Equal(t, `'primary'|'danger'`, props[1].Type)
	require.Equal(t, "Visual style", props[1].Description)
}

func TestLiveActionReferencesInTwig(t *testing.T) {
	source := `<button
    data-action="live#action"
    data-live-action-param="debounce(300)|save"
    data-live-id-param="42"
    data-live-item-name-param="Cart"
>Save</button>
<button {{ live_action('removeItem', {id: item.id}) }}>Remove</button>
<button data-live-action-param="{{ dynamicAction }}">Dynamic</button>`
	tree := twigparser.Parse(source).Tree
	references := liveActionReferencesInTwig(
		"/project/templates/components/Cart.html.twig",
		tree.Root,
	)
	require.Len(t, references, 2)
	require.Equal(t, "removeItem", references[0].Name)
	require.Equal(t, LiveActionHelperReference, references[0].Kind)
	require.Equal(
		t,
		"removeItem",
		source[references[0].Range.Start:references[0].Range.End],
	)
	require.Equal(t, "save", references[1].Name)
	require.Equal(t, LiveActionAttributeReference, references[1].Kind)
	require.Equal(
		t,
		"save",
		source[references[1].Range.Start:references[1].Range.End],
	)
	arguments := LiveActionArgumentReferencesInTwig(
		"/project/templates/components/Cart.html.twig",
		tree.Root,
	)
	require.Len(t, arguments, 3)
	require.Equal(t, "save", arguments[0].Action)
	require.Equal(t, "id", arguments[0].Name)
	require.Equal(
		t,
		"id",
		source[arguments[0].Range.Start:arguments[0].Range.End],
	)
	require.Equal(t, "itemName", arguments[1].Name)
	require.Equal(
		t,
		"item-name",
		source[arguments[1].Range.Start:arguments[1].Range.End],
	)
	require.Equal(t, "removeItem", arguments[2].Action)
	require.Equal(t, "id", arguments[2].Name)
	require.Equal(
		t,
		"id",
		source[arguments[2].Range.Start:arguments[2].Range.End],
	)
}

func TestAnonymousComponentName(t *testing.T) {
	for _, test := range []struct {
		template string
		name     string
		found    bool
	}{
		{"components/Alert.html.twig", "Alert", true},
		{"components/Nav/index.html.twig", "Nav", true},
		{"components/Nav/Item.html.twig", "Nav:Item", true},
		{"@Admin/components/Card.html.twig", "Admin:Card", true},
		{"components/index.html.twig", "", false},
		{"other/Alert.html.twig", "", false},
	} {
		name, found := anonymousComponentName(
			test.template,
			[]string{"components/"},
		)
		require.Equal(t, test.found, found, test.template)
		require.Equal(t, test.name, name, test.template)
	}
}

func TestPropUsageAt(t *testing.T) {
	source := `<twig:Alert message="Hello" :variant="kind" />`
	tree := twigparser.Parse(source).Tree
	for _, test := range []struct {
		needle  string
		name    string
		dynamic bool
	}{
		{"message", "message", false},
		{":variant", "variant", true},
	} {
		offset := uint32(strings.Index(source, test.needle) + 2)
		node := tree.Root.NodeAtOffset(offset)
		usage, found := PropUsageAt(tree.Root, node, offset)
		require.True(t, found)
		require.Equal(t, "Alert", usage.Component)
		require.Equal(t, test.name, usage.Name)
		require.Equal(t, test.dynamic, usage.Dynamic)
		require.Equal(
			t,
			test.needle,
			source[usage.Range.Start:usage.Range.End],
		)
	}
}

func TestBlockUsageAtAndReservedBlockTag(t *testing.T) {
	source := `<twig:Card><twig:block name="footer">Hi</twig:block></twig:Card>`
	tree := twigparser.Parse(source).Tree
	usages := usagesInTwig("/project/templates/page.html.twig", tree.Root)
	require.Len(t, usages, 1)
	require.Equal(t, "Card", usages[0].Name)

	offset := uint32(strings.Index(source, "footer") + 2)
	node := tree.Root.NodeAtOffset(offset)
	usage, found := BlockUsageAt(node, offset)
	require.True(t, found)
	require.Equal(t, "Card", usage.Component)
	require.Equal(t, "footer", usage.Name)
	require.Equal(
		t,
		"footer",
		source[usage.Range.Start:usage.Range.End],
	)
}
