package twigcomponent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/symfony"
	"github.com/shopware/shopware-lsp/internal/twig"
	"github.com/stretchr/testify/require"
)

func TestIndexResolvesAttributedAndAnonymousComponents(t *testing.T) {
	root := t.TempDir()
	cache := filepath.Join(root, ".cache")
	phpIndex, err := php.NewPHPIndex(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	require.NoError(t, phpIndex.ConfigureProject(root))

	twigIndex, err := twig.NewTwigIndexer(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, twigIndex.Close()) })

	componentIndex, err := NewIndex(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, componentIndex.Close()) })
	componentIndex.SetDependencies(phpIndex, nil, twigIndex)

	configPath := filepath.Join(root, "config/packages/twig_component.yaml")
	config := []byte(`twig_component:
  defaults:
    App\Pizza\Components\:
      template_directory: components/pizza
      name_prefix: Pizza
`)
	require.NoError(t, componentIndex.Index(
		indexer.NewParsedFile(configPath, config),
	))

	classPath := filepath.Join(root, "src/Pizza/Components/Button.php")
	class := []byte(`<?php
namespace App\Pizza\Components;
use Symfony\UX\TwigComponent\Attribute\AsTwigComponent;
#[AsTwigComponent]
final class Button {
    public string $label = 'Buy';
}
`)
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(classPath, class)))
	require.NoError(t, componentIndex.Index(
		indexer.NewParsedFile(classPath, class),
	))

	templatePath := filepath.Join(
		root,
		"templates/components/pizza/Button.html.twig",
	)
	template := []byte(`{# @prop variant string Visual style #}
{% props variant = 'primary' %}
<button>{{ label }}</button>`)
	require.NoError(t, twigIndex.Index(
		indexer.NewParsedFile(templatePath, template),
	))
	require.NoError(t, componentIndex.Index(
		indexer.NewParsedFile(templatePath, template),
	))

	anonymousPath := filepath.Join(
		root,
		"templates/components/Nav/index.html.twig",
	)
	require.NoError(t, twigIndex.Index(indexer.NewParsedFile(
		anonymousPath,
		[]byte("<nav></nav>"),
	)))

	usagePath := filepath.Join(root, "templates/page.html.twig")
	usage := []byte(
		`{{ component('Pizza:Button') }} <twig:Nav />`,
	)
	require.NoError(t, twigIndex.Index(
		indexer.NewParsedFile(usagePath, usage),
	))
	require.NoError(t, componentIndex.Index(
		indexer.NewParsedFile(usagePath, usage),
	))

	pizza, err := componentIndex.Find("Pizza:Button")
	require.NoError(t, err)
	require.Len(t, pizza, 1)
	require.Equal(
		t,
		"components/pizza/Button.html.twig",
		pizza[0].Template,
	)
	require.Equal(t, "App\\Pizza\\Components\\Button", pizza[0].Class)

	nav, err := componentIndex.Find("Nav")
	require.NoError(t, err)
	require.Len(t, nav, 1)
	require.Equal(t, anonymousPath, nav[0].File)

	props, err := componentIndex.Props("Pizza:Button")
	require.NoError(t, err)
	require.Len(t, props, 2)
	require.Equal(t, "label", props[0].Name)
	require.Equal(t, "string", props[0].Type)
	require.Equal(t, "variant", props[1].Name)
	require.Equal(t, "'primary'", props[1].DefaultValue)

	usages, err := componentIndex.Usages("Pizza:Button")
	require.NoError(t, err)
	require.Len(t, usages, 1)
	require.Equal(t, usagePath, usages[0].File)
}

func TestIndexMergesCustomExposedAndWritableLiveProps(t *testing.T) {
	root := t.TempDir()
	cache := filepath.Join(root, ".cache")
	phpIndex, err := php.NewPHPIndex(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	twigIndex, err := twig.NewTwigIndexer(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, twigIndex.Close()) })
	componentIndex, err := NewIndex(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, componentIndex.Close()) })
	componentIndex.SetDependencies(phpIndex, nil, twigIndex)

	classPath := filepath.Join(root, "src/Twig/Components/Search.php")
	class := []byte(`<?php
namespace App\Twig\Components;
use Symfony\UX\LiveComponent\Attribute\AsLiveComponent;
use Symfony\UX\LiveComponent\Attribute\LiveProp;
use Symfony\UX\TwigComponent\Attribute\ExposeInTemplate;
use App\Entity\Product;
#[AsLiveComponent]
final class Search {
    #[ExposeInTemplate(name: 'headline')]
    private Product $title;

    #[LiveProp(writable: true)]
    public string $query = '';

    public function getResults(): array {}
}`)
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		classPath,
		class,
	)))
	require.NoError(t, componentIndex.Index(indexer.NewParsedFile(
		classPath,
		class,
	)))
	templatePath := filepath.Join(
		root,
		"templates/components/Search.html.twig",
	)
	require.NoError(t, twigIndex.Index(indexer.NewParsedFile(
		templatePath,
		[]byte(`{{ headline }} {{ query }} {{ computed.results }}`),
	)))

	components, err := componentIndex.Find("Search")
	require.NoError(t, err)
	require.NotEmpty(t, components)
	var liveComponent bool
	for _, component := range components {
		liveComponent = liveComponent || component.Live
	}
	require.True(t, liveComponent)
	props, err := componentIndex.Props("Search")
	require.NoError(t, err)
	var headline, query Prop
	for _, prop := range props {
		switch prop.Name {
		case "headline":
			headline = prop
		case "query":
			query = prop
		case "title":
			t.Fatal("custom ExposeInTemplate mapping leaked PHP member name")
		}
	}
	require.Equal(t, "App\\Entity\\Product", headline.Type)
	require.Equal(t, "title", headline.Member)
	require.True(t, query.Live)
	require.True(t, query.Writable)

	computed, err := componentIndex.Computed("Search")
	require.NoError(t, err)
	require.Len(t, computed, 1)
	require.Equal(t, "results", computed[0].Name)
	require.Equal(t, "array", computed[0].Type)
}

func TestIndexResolvesInheritedLiveActionsAndReferences(t *testing.T) {
	root := t.TempDir()
	cache := filepath.Join(root, ".cache")
	phpIndex, err := php.NewPHPIndex(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	twigIndex, err := twig.NewTwigIndexer(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, twigIndex.Close()) })
	componentIndex, err := NewIndex(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, componentIndex.Close()) })
	componentIndex.SetDependencies(phpIndex, nil, twigIndex)

	basePath := filepath.Join(root, "src/Twig/Components/BaseCart.php")
	base := []byte(`<?php
namespace App\Twig\Components;
use Symfony\UX\LiveComponent\Attribute\LiveAction;
use Symfony\UX\LiveComponent\Attribute\LiveArg;
abstract class BaseCart {
    #[LiveAction]
    public function save(
        #[LiveArg('itemId')] int $id,
        ?string $note = null,
    ): void {}

    #[LiveAction]
    protected function hidden(): void {}

    #[LiveAction]
    public static function invalidStatic(): void {}
}`)
	classPath := filepath.Join(root, "src/Twig/Components/Cart.php")
	class := []byte(`<?php
namespace App\Twig\Components;
use Symfony\UX\LiveComponent\Attribute\AsLiveComponent;
#[AsLiveComponent]
final class Cart extends BaseCart {}`)
	for path, source := range map[string][]byte{
		basePath:  base,
		classPath: class,
	} {
		require.NoError(t, phpIndex.Index(indexer.NewParsedFile(path, source)))
		require.NoError(t, componentIndex.Index(
			indexer.NewParsedFile(path, source),
		))
	}

	templatePath := filepath.Join(
		root,
		"templates/components/Cart.html.twig",
	)
	template := []byte(
		`<button data-live-action-param="save">Save</button>`,
	)
	require.NoError(t, twigIndex.Index(indexer.NewParsedFile(
		templatePath,
		template,
	)))
	require.NoError(t, componentIndex.Index(indexer.NewParsedFile(
		templatePath,
		template,
	)))

	actions, err := componentIndex.LiveActions("Cart")
	require.NoError(t, err)
	require.Len(t, actions, 1)
	require.Equal(t, "save", actions[0].Name)
	require.Equal(t, basePath, actions[0].File)
	require.Len(t, actions[0].Parameters, 2)
	require.Equal(t, "itemId", actions[0].Parameters[0].Name)
	require.Equal(t, "id", actions[0].Parameters[0].PHPName)
	require.Equal(t, "int", actions[0].Parameters[0].Type)
	require.True(t, actions[0].Parameters[1].Optional)

	references, err := componentIndex.LiveActionReferences("Cart", "save")
	require.NoError(t, err)
	require.Len(t, references, 1)
	require.Equal(t, templatePath, references[0].File)
	require.Equal(t, "save", references[0].Name)
}

func TestIndexResolvesInheritedRepeatableLiveListenersAndEmissions(t *testing.T) {
	root := t.TempDir()
	cache := filepath.Join(root, ".cache")
	phpIndex, err := php.NewPHPIndex(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	twigIndex, err := twig.NewTwigIndexer(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, twigIndex.Close()) })
	componentIndex, err := NewIndex(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, componentIndex.Close()) })
	componentIndex.SetDependencies(phpIndex, nil, twigIndex)

	basePath := filepath.Join(root, "src/Twig/Components/BaseCart.php")
	base := []byte(`<?php
namespace App\Twig\Components;
use Symfony\UX\LiveComponent\Attribute\LiveArg;
use Symfony\UX\LiveComponent\Attribute\LiveListener;
abstract class BaseCart {
    #[LiveListener('productAdded')]
    #[LiveListener('productRestored')]
    public function refresh(
        #[LiveArg('productId')] int $id,
        string $source = 'shop',
    ): void {}

    #[LiveListener('hidden')]
    private function hidden(): void {}
}`)
	classPath := filepath.Join(root, "src/Twig/Components/Cart.php")
	class := []byte(`<?php
namespace App\Twig\Components;
use Symfony\UX\LiveComponent\Attribute\AsLiveComponent;
#[AsLiveComponent]
final class Cart extends BaseCart {
    public function save(): void {
        $this->emit('productAdded', ['productId' => 42]);
    }
}`)
	for path, source := range map[string][]byte{
		basePath:  base,
		classPath: class,
	} {
		require.NoError(t, phpIndex.Index(indexer.NewParsedFile(path, source)))
		require.NoError(t, componentIndex.Index(
			indexer.NewParsedFile(path, source),
		))
	}

	templatePath := filepath.Join(
		root,
		"templates/components/Cart.html.twig",
	)
	template := []byte(`<button
    data-action="live#emitSelf"
    data-live-event-param="productRestored"
    data-live-product-id-param="42"
></button>`)
	require.NoError(t, twigIndex.Index(indexer.NewParsedFile(
		templatePath,
		template,
	)))
	require.NoError(t, componentIndex.Index(indexer.NewParsedFile(
		templatePath,
		template,
	)))

	listeners, err := componentIndex.LiveListeners()
	require.NoError(t, err)
	require.Len(t, listeners, 2)
	require.Equal(t, "productAdded", listeners[0].Name)
	require.Equal(t, basePath, listeners[0].File)
	require.Equal(t, "refresh", listeners[0].Method)
	require.Len(t, listeners[0].Parameters, 2)
	require.Equal(t, "productId", listeners[0].Parameters[0].Name)
	require.Equal(t, "id", listeners[0].Parameters[0].PHPName)
	require.True(t, listeners[0].Parameters[1].Optional)

	references, err := componentIndex.LiveEventReferences("productAdded")
	require.NoError(t, err)
	require.Len(t, references, 1)
	require.Equal(t, classPath, references[0].File)

	references, err = componentIndex.LiveEventReferences("productRestored")
	require.NoError(t, err)
	require.Len(t, references, 1)
	require.Equal(t, templatePath, references[0].File)

	names, err := componentIndex.LiveEventNames()
	require.NoError(t, err)
	require.Equal(t, []string{"productAdded", "productRestored"}, names)
}

func TestIndexRestoresAndClearsCandidateRecords(t *testing.T) {
	cache := t.TempDir()
	path := filepath.Join(cache, "templates/page.html.twig")
	first, err := NewIndex(cache)
	require.NoError(t, err)
	require.NoError(t, first.Index(indexer.NewParsedFile(
		path,
		[]byte(`{{ component('Alert') }}`),
	)))
	require.NoError(t, first.Close())

	restored, err := NewIndex(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, restored.Close()) })
	usages, err := restored.Usages("Alert")
	require.NoError(t, err)
	require.Len(t, usages, 1)

	require.NoError(t, restored.Index(indexer.NewParsedFile(
		path,
		[]byte(`plain text`),
	)))
	usages, err = restored.Usages("Alert")
	require.NoError(t, err)
	require.Empty(t, usages)
}

func TestIndexDiscoversModernAndLegacyServiceTags(t *testing.T) {
	root := t.TempDir()
	cache := filepath.Join(root, ".cache")
	phpIndex, err := php.NewPHPIndex(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	require.NoError(t, phpIndex.ConfigureProject(root))

	serviceIndex, err := symfony.NewServiceIndex(root, cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, serviceIndex.Close()) })
	serviceIndex.SetPHPIndex(phpIndex)

	servicesPath := filepath.Join(root, "config/services.yaml")
	require.NoError(t, serviceIndex.Index(indexer.NewParsedFile(
		servicesPath,
		[]byte(`services:
  App\Twig\Components\Alert:
    tags:
      - ux.twig_component
  App\Twig\Components\Legacy:
    tags:
      - twig.component
`),
	)))

	componentIndex, err := NewIndex(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, componentIndex.Close()) })
	componentIndex.SetDependencies(phpIndex, serviceIndex, nil)
	names, err := componentIndex.Names()
	require.NoError(t, err)
	require.Contains(t, names, "Alert")
	require.Contains(t, names, "Legacy")
}

func TestIndexResolvesPHPConfiguredComponentDefaults(t *testing.T) {
	root := t.TempDir()
	cache := filepath.Join(root, ".cache")
	phpIndex, err := php.NewPHPIndex(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	require.NoError(t, phpIndex.ConfigureProject(root))
	twigIndex, err := twig.NewTwigIndexer(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, twigIndex.Close()) })
	componentIndex, err := NewIndex(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, componentIndex.Close()) })
	componentIndex.SetDependencies(phpIndex, nil, twigIndex)

	configPath := filepath.Join(root, "config/packages/twig_component.php")
	require.NoError(t, componentIndex.Index(indexer.NewParsedFile(
		configPath,
		[]byte(`<?php
return [
    'twig_component' => [
        'defaults' => [
            'App\\Pizza\\Components\\' => [
                'template_directory' => 'components/pizza',
                'name_prefix' => 'Pizza',
            ],
        ],
        'anonymous_template_directory' => 'ux-components',
    ],
];`),
	)))
	classPath := filepath.Join(
		root,
		"src/Pizza/Components/Button/Primary.php",
	)
	class := []byte(`<?php
namespace App\Pizza\Components\Button;
use Symfony\UX\TwigComponent\Attribute\AsTwigComponent;
#[AsTwigComponent]
final class Primary {}
`)
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		classPath,
		class,
	)))
	require.NoError(t, componentIndex.Index(indexer.NewParsedFile(
		classPath,
		class,
	)))
	templatePath := filepath.Join(
		root,
		"templates/ux-components/Alert.html.twig",
	)
	require.NoError(t, twigIndex.Index(indexer.NewParsedFile(
		templatePath,
		[]byte("<div></div>"),
	)))

	components, err := componentIndex.Find("Pizza:Button:Primary")
	require.NoError(t, err)
	require.Len(t, components, 1)
	require.Equal(
		t,
		"components/pizza/Button/Primary.html.twig",
		components[0].Template,
	)
	alerts, err := componentIndex.Find("Alert")
	require.NoError(t, err)
	require.Len(t, alerts, 1)
	require.Equal(t, templatePath, alerts[0].File)
}

func TestIndexUsesCompiledContainerTwigComponents(t *testing.T) {
	root := t.TempDir()
	cache := filepath.Join(root, ".cache")
	containerDir := filepath.Join(root, "var", "cache", "dev-test")
	require.NoError(t, os.MkdirAll(containerDir, 0o755))
	containerPath := filepath.Join(
		containerDir,
		"Shopware_Core_KernelDevDebugContainer.xml",
	)
	require.NoError(t, os.WriteFile(
		containerPath,
		[]byte(compiledTwigComponentContainer(
			`<argument key="CompiledOnly" type="collection">
  <argument key="class">App\Twig\Components\CompiledOnly</argument>
  <argument key="template">compiled/Only.html.twig</argument>
</argument>
<argument key="Override" type="collection">
  <argument key="class">App\Twig\Components\Override</argument>
  <argument key="template">compiled/Override.html.twig</argument>
</argument>
<argument key="Dynamic" type="collection">
  <argument key="class">App\Twig\Components\Dynamic</argument>
  <argument key="template_from_method">getTemplate</argument>
</argument>`,
			`<argument key="App\Twig\Components\CompiledOnly">CompiledOnly</argument>
<argument key="App\Twig\Components\Override">Override</argument>
<argument key="App\Twig\Components\Dynamic">Dynamic</argument>`,
		)),
		0o644,
	))

	phpIndex, err := php.NewPHPIndex(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	require.NoError(t, phpIndex.ConfigureProject(root))
	twigIndex, err := twig.NewTwigIndexer(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, twigIndex.Close()) })
	serviceIndex, err := symfony.NewServiceIndex(root, cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, serviceIndex.Close()) })
	componentIndex, err := NewIndex(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, componentIndex.Close()) })
	componentIndex.SetDependencies(phpIndex, serviceIndex, twigIndex)

	configPath := filepath.Join(
		root,
		"config/packages/twig_component.yaml",
	)
	require.NoError(t, componentIndex.Index(indexer.NewParsedFile(
		configPath,
		[]byte(`twig_component:
  defaults:
    App\Twig\Components\:
      template_directory: components/from-yaml
`),
	)))
	classPath := filepath.Join(
		root,
		"src/Twig/Components/Components.php",
	)
	class := []byte(`<?php
namespace App\Twig\Components;
use Symfony\UX\TwigComponent\Attribute\AsTwigComponent;
final class CompiledOnly {
    public string $title = 'hello';
}
#[AsTwigComponent(name: 'Override')]
final class Override {}
final class Dynamic {}
`)
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		classPath,
		class,
	)))
	require.NoError(t, componentIndex.Index(indexer.NewParsedFile(
		classPath,
		class,
	)))

	templatePath := filepath.Join(
		root,
		"templates/compiled/Only.html.twig",
	)
	require.NoError(t, twigIndex.Index(indexer.NewParsedFile(
		templatePath,
		[]byte(`{{ title }}`),
	)))

	compiledOnly, err := componentIndex.Find("CompiledOnly")
	require.NoError(t, err)
	require.Len(t, compiledOnly, 1)
	assertCompiledComponent(
		t,
		compiledOnly[0],
		"App\\Twig\\Components\\CompiledOnly",
		"compiled/Only.html.twig",
	)
	props, err := componentIndex.Props("CompiledOnly")
	require.NoError(t, err)
	require.Len(t, props, 1)
	require.Equal(t, "title", props[0].Name)

	override, err := componentIndex.Find("Override")
	require.NoError(t, err)
	require.Len(t, override, 1)
	assertCompiledComponent(
		t,
		override[0],
		"App\\Twig\\Components\\Override",
		"compiled/Override.html.twig",
	)

	dynamic, err := componentIndex.Find("Dynamic")
	require.NoError(t, err)
	require.Len(t, dynamic, 1)
	require.Empty(t, dynamic[0].Template)
	require.Equal(t, "getTemplate", dynamic[0].TemplateFromMethod)
	require.Equal(t, CompiledContainerSource, dynamic[0].Source)

	reverse, err := componentIndex.ComponentsForTemplate(templatePath)
	require.NoError(t, err)
	require.Len(t, reverse, 1)
	require.Equal(t, "CompiledOnly", reverse[0].Name)

	require.NoError(t, os.WriteFile(
		containerPath,
		[]byte(compiledTwigComponentContainer(
			`<argument key="Reloaded" type="collection">
  <argument key="class">App\Twig\Components\Reloaded</argument>
  <argument key="template">compiled/Reloaded.html.twig</argument>
</argument>`,
			`<argument key="App\Twig\Components\Reloaded">Reloaded</argument>`,
		)),
		0o644,
	))
	require.NoError(t, serviceIndex.ReloadCompiledContainer())
	reloaded, err := componentIndex.Find("Reloaded")
	require.NoError(t, err)
	require.Len(t, reloaded, 1)
	require.Equal(t, "compiled/Reloaded.html.twig", reloaded[0].Template)
}

func compiledTwigComponentContainer(
	components,
	classMap string,
) string {
	return `<container><services>
<service id="ux.twig_component.component_factory">
  <argument/><argument/><argument/><argument/>
  <argument type="collection">` + components + `</argument>
  <argument type="collection">` + classMap + `</argument>
</service>
</services></container>`
}

func assertCompiledComponent(
	t *testing.T,
	component Component,
	class,
	template string,
) {
	t.Helper()
	require.Equal(t, class, component.Class)
	require.Equal(t, template, component.Template)
	require.Equal(t, CompiledContainerSource, component.Source)
}
