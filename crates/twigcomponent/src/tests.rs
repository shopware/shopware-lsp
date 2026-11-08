//! Ports of `parser_test.go` and `index_test.go`.

use std::collections::HashMap;
use std::path::Path;
use std::sync::Arc;

use swlsp_indexer::{Indexer, ParsedFile};
use swlsp_php::PhpIndex;
use swlsp_symfony::ServiceIndex;
use swlsp_syntax::{SyntaxNode, SyntaxNodeExt, TextRange, TextSize};
use swlsp_twig::TwigIndexer;

use crate::config::{configuration_in_php, configuration_in_yaml};
use crate::index::anonymous_component_name;
use crate::php::{declarations_in_php, props_in_php};
use crate::twig::{live_action_references_in_twig_impl, props_in_twig_impl, usages_in_twig_impl};
use crate::*;

fn php(source: &str) -> SyntaxNode {
    swlsp_parser_php::parse(source).tree.root()
}

fn twig(source: &str) -> SyntaxNode {
    swlsp_parser_twig::parse(source).tree.root()
}

fn yaml(source: &str) -> SyntaxNode {
    swlsp_parser_yaml::parse(source).tree.root()
}

fn slice(source: &str, range: TextRange) -> &str {
    &source[usize::from(range.start())..usize::from(range.end())]
}

fn find(source: &str, needle: &str) -> u32 {
    source.find(needle).unwrap() as u32
}

#[test]
fn declarations_in_php_test() {
    let source = r"<?php
namespace App\Twig\Components;

use Symfony\UX\TwigComponent\Attribute\AsTwigComponent as Component;

#[Component(name: 'Shop:Card', template: 'ui/card.html.twig', exposePublicProps: false)]
final class Card {}
";
    let declarations = declarations_in_php("/project/src/Card.php", &php(source));
    assert_eq!(declarations.len(), 1);
    let declaration = &declarations[0];
    assert_eq!(declaration.name, "Shop:Card");
    assert_eq!(declaration.class, "App\\Twig\\Components\\Card");
    assert_eq!(declaration.template, "ui/card.html.twig");
    assert!(!declaration.expose_public_props);
    assert_eq!(slice(source, declaration.name_range), "Shop:Card");
    assert_eq!(slice(source, declaration.class_range), "Card");
}

#[test]
fn php_props_preserve_expose_mappings_and_live_writability() {
    let source = r"<?php
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
";
    let root = php(source);
    let declarations = declarations_in_php("/project/src/Search.php", &root);
    assert_eq!(declarations.len(), 1);
    assert!(declarations[0].live);

    let props = props_in_php("/project/src/Search.php", &root);
    assert_eq!(props.len(), 4);
    let by_name: HashMap<&str, &Prop> = props
        .iter()
        .map(|prop| (prop.name.as_str(), prop))
        .collect();
    for prop in &props {
        assert_eq!(prop.class, "App\\Twig\\Components\\Search");
    }
    assert_eq!(by_name["headline"].ty, "string");
    assert_eq!(by_name["icon"].ty, "string");
    assert_eq!(by_name["state"].ty, "string");
    assert!(by_name["query"].live);
    assert!(by_name["query"].writable);
    assert_eq!(slice(source, by_name["query"].range), "$query");
}

#[test]
fn live_listener_declarations_and_php_emissions() {
    let source = r#"<?php
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
"#;
    let root = php(source);
    let listeners = live_listeners_in_php("/project/src/Cart.php", &root);
    assert_eq!(listeners.len(), 2);
    assert_eq!(listeners[0].name, "productAdded");
    assert_eq!(listeners[1].name, "productRestored");
    assert_eq!(listeners[0].method, "refresh");
    assert_eq!(listeners[0].parameters.len(), 2);
    assert_eq!(listeners[0].parameters[0].name, "productId");
    assert_eq!(listeners[0].parameters[0].php_name, "id");
    assert_eq!(slice(source, listeners[0].range), "productAdded");

    let references = live_event_references_in_php("/project/src/Cart.php", &root);
    assert_eq!(references.len(), 2);
    assert_eq!(references[0].name, "productAdded");
    assert_eq!(references[0].kind, LiveEventReferenceKind::Emit);
    assert_eq!(references[0].class, "App\\Twig\\Components\\Cart");
    assert_eq!(references[1].name, "productRestored");
    assert_eq!(references[1].kind, LiveEventReferenceKind::EmitUp);

    let arguments = live_event_argument_references_in_php("/project/src/Cart.php", &root);
    assert_eq!(arguments.len(), 2);
    assert_eq!(arguments[0].name, "productId");
    assert_eq!(arguments[1].name, "source");

    let offset = find(source, "'productId' =>") + 3;
    let node = root.node_at(TextSize::from(offset)).unwrap();
    let (event, present) = live_event_argument_context_php(&node).unwrap();
    assert_eq!(event, "productAdded");
    assert_eq!(present, vec!["productId".to_owned()]);
    assert!(
        live_listener_at_php(
            "/project/src/Cart.php",
            &root,
            find(source, "productRestored") + 1
        )
        .is_some()
    );
}

#[test]
fn live_event_references_in_twig_require_emit_action() {
    let source = r#"<button
    data-action="click->live#emit"
    data-live-event-param="name(ProductList)|productAdded"
    data-live-product-id-param="42"
></button>
<button
    data-action="live#emitSelf"
    data-live-event-param="refreshed"
></button>
<button data-live-event-param="ignored"></button>
<button data-action="live#emit" data-live-event-param="{{ dynamic }}"></button>"#;
    let root = twig(source);
    let references = live_event_references_in_twig("/project/templates/Cart.html.twig", &root);
    assert_eq!(references.len(), 2);
    assert_eq!(references[0].name, "productAdded");
    assert_eq!(references[0].kind, LiveEventReferenceKind::Attribute);
    assert_eq!(slice(source, references[0].range), "productAdded");
    assert_eq!(references[0].context_range, TextRange::default());
    assert_eq!(references[1].name, "refreshed");
    assert_eq!(references[1].kind, LiveEventReferenceKind::EmitSelf);

    let arguments =
        live_event_argument_references_in_twig("/project/templates/Cart.html.twig", &root);
    assert_eq!(arguments.len(), 1);
    assert_eq!(arguments[0].event, "productAdded");
    assert_eq!(arguments[0].name, "productId");
}

#[test]
fn configuration_in_yaml_test() {
    let source = r"twig_component:
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
";
    let (namespaces, anonymous) = configuration_in_yaml(
        "/project/config/packages/twig_component.yaml",
        &yaml(source),
    );
    assert_eq!(anonymous, vec!["ui/components/".to_owned()]);
    assert_eq!(namespaces.len(), 3);
    assert_eq!(namespaces[0].class_prefix, "App\\Twig\\Components");
    assert_eq!(namespaces[0].template_directory, "components/");
    assert_eq!(
        slice(source, namespaces[0].range),
        "App\\Twig\\Components\\"
    );
    assert_eq!(namespaces[1].name_prefix, "Pizza");
    assert_eq!(namespaces[2].template_directory, "test/components/");
}

fn by_class(namespaces: &[Namespace]) -> HashMap<&str, &Namespace> {
    namespaces
        .iter()
        .map(|namespace| (namespace.class_prefix.as_str(), namespace))
        .collect()
}

#[test]
fn configuration_in_php_array_forms() {
    let source = r"<?php
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
];";
    let (namespaces, anonymous) =
        configuration_in_php("/project/config/packages/twig_component.php", &php(source));
    assert_eq!(anonymous, vec!["php_components/".to_owned()]);
    assert_eq!(namespaces.len(), 4);
    let classes = by_class(&namespaces);
    assert_eq!(
        classes["App\\PhpShort\\Components"].template_directory,
        "php/components/"
    );
    assert_eq!(
        classes["App\\PhpLong\\Components"].template_directory,
        "php/long/"
    );
    assert_eq!(classes["App\\PhpLong\\Components"].name_prefix, "PhpLong");
    assert_eq!(
        classes["App\\PhpDefaulted\\Components"].template_directory,
        "components/"
    );
    assert_eq!(
        classes["App\\PhpWhen\\Components"].template_directory,
        "php/when/"
    );
}

#[test]
fn configuration_in_php_app_config_forms() {
    let source = r"<?php
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
]);";
    let (namespaces, anonymous) =
        configuration_in_php("/project/config/packages/twig_component.php", &php(source));
    assert!(anonymous.is_empty());
    assert_eq!(namespaces.len(), 2);
    let classes = by_class(&namespaces);
    assert_eq!(
        classes["App\\Statement\\Components"].template_directory,
        "statement/components/"
    );
    assert_eq!(
        classes["App\\Returned\\Components"].template_directory,
        "returned/components/"
    );
}

#[test]
fn configuration_in_php_container_extension_closure() {
    let source = r"<?php
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
};";
    let (namespaces, anonymous) =
        configuration_in_php("/project/config/packages/twig_component.php", &php(source));
    assert_eq!(anonymous, vec!["ux-components/".to_owned()]);
    assert_eq!(namespaces.len(), 1);
    assert_eq!(namespaces[0].class_prefix, "App\\Shared\\Ui\\Component");
    assert_eq!(namespaces[0].template_directory, "@Shared/");
    assert_eq!(namespaces[0].name_prefix, "Shared");
}

#[test]
fn configuration_in_php_ignores_nested_and_unrelated_configs() {
    let source = r"<?php
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
];";
    let (namespaces, anonymous) =
        configuration_in_php("/project/config/packages/twig_component.php", &php(source));
    assert!(namespaces.is_empty());
    assert!(anonymous.is_empty());
}

#[test]
fn usages_and_props_in_twig() {
    let source = r"{# @prop variant 'primary'|'danger' Visual style #}
{% props icon, variant = 'primary' %}
{{ component('Alert', {icon: icon}) }}
{% component 'Modal' %}{% endcomponent %}
{% component Notice with {type: 'success'} %}{% endcomponent %}
<twig:Nav:Item :active=true />";
    let root = twig(source);
    let usages = usages_in_twig_impl("/project/templates/page.html.twig", &root);
    assert_eq!(usages.len(), 4);
    let expected = [
        ("Alert", UsageKind::Function),
        ("Modal", UsageKind::Block),
        ("Notice", UsageKind::Block),
        ("Nav:Item", UsageKind::Html),
    ];
    for (usage, (name, kind)) in usages.iter().zip(expected) {
        assert_eq!(usage.name, name);
        assert_eq!(usage.kind, kind);
        assert_eq!(slice(source, usage.range), usage.name);
    }

    let props = props_in_twig_impl("/project/templates/components/Alert.html.twig", &root);
    assert_eq!(props.len(), 2);
    assert_eq!(props[0].name, "icon");
    assert!(props[0].default_value.is_empty());
    assert_eq!(props[1].name, "variant");
    assert_eq!(props[1].default_value, "'primary'");
    assert_eq!(props[1].ty, "'primary'|'danger'");
    assert_eq!(props[1].description, "Visual style");
}

#[test]
fn live_action_references_in_twig_test() {
    let source = r#"<button
    data-action="live#action"
    data-live-action-param="debounce(300)|save"
    data-live-id-param="42"
    data-live-item-name-param="Cart"
>Save</button>
<button {{ live_action('removeItem', {id: item.id}) }}>Remove</button>
<button data-live-action-param="{{ dynamicAction }}">Dynamic</button>"#;
    let root = twig(source);
    let path = "/project/templates/components/Cart.html.twig";
    let references = live_action_references_in_twig_impl(path, &root);
    assert_eq!(references.len(), 2);
    assert_eq!(references[0].name, "removeItem");
    assert_eq!(references[0].kind, LiveActionReferenceKind::Helper);
    assert_eq!(slice(source, references[0].range), "removeItem");
    assert_eq!(references[1].name, "save");
    assert_eq!(references[1].kind, LiveActionReferenceKind::Attribute);
    assert_eq!(slice(source, references[1].range), "save");

    let arguments = live_action_argument_references_in_twig(path, &root);
    assert_eq!(arguments.len(), 3);
    assert_eq!(arguments[0].action, "save");
    assert_eq!(arguments[0].name, "id");
    assert_eq!(slice(source, arguments[0].range), "id");
    assert_eq!(arguments[1].name, "itemName");
    assert_eq!(slice(source, arguments[1].range), "item-name");
    assert_eq!(arguments[2].action, "removeItem");
    assert_eq!(arguments[2].name, "id");
    assert_eq!(slice(source, arguments[2].range), "id");

    let offset = find(source, "{id:") + 1;
    let node = root.node_at(TextSize::from(offset)).unwrap();
    let (action, present) = live_action_argument_context(&node).unwrap();
    assert_eq!(action, "removeItem");
    assert_eq!(present, vec!["id".to_owned()]);
    assert_eq!(
        live_action_reference_at(path, &root, find(source, "save") + 1)
            .map(|reference| reference.name),
        Some("save".to_owned())
    );
}

#[test]
fn anonymous_component_name_test() {
    let directories = vec!["components/".to_owned()];
    for (template, expected) in [
        ("components/Alert.html.twig", Some("Alert")),
        ("components/Nav/index.html.twig", Some("Nav")),
        ("components/Nav/Item.html.twig", Some("Nav:Item")),
        ("@Admin/components/Card.html.twig", Some("Admin:Card")),
        ("components/index.html.twig", None),
        ("other/Alert.html.twig", None),
    ] {
        assert_eq!(
            anonymous_component_name(template, &directories).as_deref(),
            expected,
            "{template}"
        );
    }
}

#[test]
fn prop_usage_at_test() {
    let source = r#"<twig:Alert message="Hello" :variant="kind" />"#;
    let root = twig(source);
    for (needle, name, dynamic) in [("message", "message", false), (":variant", "variant", true)] {
        let offset = find(source, needle) + 2;
        let node = root.node_at(TextSize::from(offset)).unwrap();
        let usage = prop_usage_at(&root, &node, offset).unwrap();
        assert_eq!(usage.component, "Alert");
        assert_eq!(usage.name, name);
        assert_eq!(usage.dynamic, dynamic);
        assert_eq!(slice(source, usage.range), needle);
    }
}

#[test]
fn block_usage_at_and_reserved_block_tag() {
    let source = r#"<twig:Card><twig:block name="footer">Hi</twig:block></twig:Card>"#;
    let root = twig(source);
    let usages = usages_in_twig_impl("/project/templates/page.html.twig", &root);
    assert_eq!(usages.len(), 1);
    assert_eq!(usages[0].name, "Card");

    let offset = find(source, "footer") + 2;
    let node = root.node_at(TextSize::from(offset)).unwrap();
    let usage = block_usage_at(&node, offset).unwrap();
    assert_eq!(usage.component, "Card");
    assert_eq!(usage.name, "footer");
    assert_eq!(slice(source, usage.range), "footer");
    assert_eq!(block_usages_in_twig(&root), vec![usage]);
}

#[test]
fn variable_and_accessor_member_at() {
    let source = "{{ computed.total.amount }}";
    let root = twig(source);
    let at = |needle: &str| {
        root.node_at(TextSize::from(find(source, needle) + 1))
            .unwrap()
    };
    assert_eq!(
        variable_at(&at("computed")).map(|(name, _)| name),
        Some("computed".to_owned())
    );
    assert!(variable_at(&at("total")).is_none());
    let (object, member, range) = accessor_member_at(&at("total")).unwrap();
    assert_eq!((object.as_str(), member.as_str()), ("computed", "total"));
    assert_eq!(slice(source, range), "total");
    assert!(accessor_member_at(&at("amount")).is_none());
}

// --- index ---------------------------------------------------------------

struct Fixture {
    _dir: tempfile::TempDir,
    root: std::path::PathBuf,
    php: PhpIndex,
    twig: Arc<TwigIndexer>,
    components: Index,
}

impl Fixture {
    fn new(configure: bool) -> Self {
        Self::with(configure, |_, _, _| None)
    }

    fn with(
        configure: bool,
        services: impl FnOnce(&Path, &Path, &PhpIndex) -> Option<Arc<ServiceIndex>>,
    ) -> Self {
        let dir = tempfile::tempdir().unwrap();
        let root = dir.path().to_path_buf();
        let cache = root.join(".cache");
        let php = PhpIndex::open(&cache, None).unwrap();
        if configure {
            php.configure_project(&root).unwrap();
        }
        let twig = Arc::new(TwigIndexer::new(&cache, None).unwrap());
        let services = services(&root, &cache, &php);
        let components = Index::new(&cache, None).unwrap();
        components.set_dependencies(Some(php.clone()), services, Some(twig.clone()));
        Fixture {
            _dir: dir,
            root,
            php,
            twig,
            components,
        }
    }

    fn path(&self, relative: &str) -> String {
        self.root.join(relative).to_string_lossy().into_owned()
    }

    fn php_file(&self, relative: &str, source: &str) -> String {
        let path = self.path(relative);
        self.php.index(&file(&path, source)).unwrap();
        self.components.index(&file(&path, source)).unwrap();
        path
    }

    fn template(&self, relative: &str, source: &str, components: bool) -> String {
        let path = self.path(relative);
        self.twig.index(&file(&path, source)).unwrap();
        if components {
            self.components.index(&file(&path, source)).unwrap();
        }
        path
    }

    fn config(&self, relative: &str, source: &str) {
        self.components
            .index(&file(&self.path(relative), source))
            .unwrap();
    }
}

impl Drop for Fixture {
    fn drop(&mut self) {
        let _ = self.components.close();
        let _ = self.twig.close();
        let _ = self.php.close();
    }
}

fn file(path: &str, source: &str) -> ParsedFile {
    ParsedFile::new(path, source.as_bytes().to_vec())
}

#[test]
fn index_resolves_attributed_and_anonymous_components() {
    let fixture = Fixture::new(true);
    fixture.config(
        "config/packages/twig_component.yaml",
        r"twig_component:
  defaults:
    App\Pizza\Components\:
      template_directory: components/pizza
      name_prefix: Pizza
",
    );
    fixture.php_file(
        "src/Pizza/Components/Button.php",
        r"<?php
namespace App\Pizza\Components;
use Symfony\UX\TwigComponent\Attribute\AsTwigComponent;
#[AsTwigComponent]
final class Button {
    public string $label = 'Buy';
}
",
    );
    fixture.template(
        "templates/components/pizza/Button.html.twig",
        "{# @prop variant string Visual style #}\n{% props variant = 'primary' %}\n<button>{{ label }}</button>",
        true,
    );
    let anonymous = fixture.template(
        "templates/components/Nav/index.html.twig",
        "<nav></nav>",
        false,
    );
    let usage = fixture.template(
        "templates/page.html.twig",
        "{{ component('Pizza:Button') }} <twig:Nav />",
        true,
    );

    let pizza = fixture.components.find("Pizza:Button").unwrap();
    assert_eq!(pizza.len(), 1);
    assert_eq!(pizza[0].template, "components/pizza/Button.html.twig");
    assert_eq!(pizza[0].class, "App\\Pizza\\Components\\Button");

    let nav = fixture.components.find("Nav").unwrap();
    assert_eq!(nav.len(), 1);
    assert_eq!(nav[0].file, anonymous);

    let props = fixture.components.props("Pizza:Button").unwrap();
    assert_eq!(props.len(), 2);
    assert_eq!(props[0].name, "label");
    assert_eq!(props[0].ty, "string");
    assert_eq!(props[1].name, "variant");
    assert_eq!(props[1].default_value, "'primary'");
    assert_eq!(props[1].ty, "string");
    assert_eq!(props[1].description, "Visual style");
    // Cached result.
    assert_eq!(fixture.components.props("Pizza:Button").unwrap(), props);

    let usages = fixture.components.usages("Pizza:Button").unwrap();
    assert_eq!(usages.len(), 1);
    assert_eq!(usages[0].file, usage);

    // Like Go, the template below the default anonymous directory also
    // registers an anonymous `pizza:Button`.
    let names = fixture.components.names().unwrap();
    assert_eq!(names, ["Nav", "Pizza:Button", "pizza:Button"]);
}

#[test]
fn index_merges_custom_exposed_and_writable_live_props() {
    let fixture = Fixture::new(false);
    fixture.php_file(
        "src/Twig/Components/Search.php",
        r"<?php
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
}",
    );
    let template = fixture.template(
        "templates/components/Search.html.twig",
        "{{ headline }} {{ query }} {{ computed.results }}",
        false,
    );

    let components = fixture.components.find("Search").unwrap();
    assert!(components.iter().any(|component| component.live));
    let props = fixture.components.props("Search").unwrap();
    assert!(
        props.iter().all(|prop| prop.name != "title"),
        "custom ExposeInTemplate mapping leaked PHP member name"
    );
    let headline = props.iter().find(|prop| prop.name == "headline").unwrap();
    let query = props.iter().find(|prop| prop.name == "query").unwrap();
    assert_eq!(headline.ty, "App\\Entity\\Product");
    assert_eq!(headline.member, "title");
    assert!(query.live);
    assert!(query.writable);

    let computed = fixture.components.computed("Search").unwrap();
    assert_eq!(computed.len(), 1);
    assert_eq!(computed[0].name, "results");
    assert_eq!(computed[0].ty, "array");
    assert_eq!(
        fixture.components.computed_for_template(&template).unwrap(),
        computed
    );

    let (context_components, context_props) = fixture
        .components
        .context_for_template(&template, None)
        .unwrap();
    // The attributed component and the anonymous one of the same template.
    assert_eq!(context_components.len(), 2);
    assert!(context_props.iter().any(|prop| prop.name == "headline"));
}

#[test]
fn index_resolves_inherited_live_actions_and_references() {
    let fixture = Fixture::new(false);
    let base = fixture.php_file(
        "src/Twig/Components/BaseCart.php",
        r"<?php
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
}",
    );
    fixture.php_file(
        "src/Twig/Components/Cart.php",
        r"<?php
namespace App\Twig\Components;
use Symfony\UX\LiveComponent\Attribute\AsLiveComponent;
#[AsLiveComponent]
final class Cart extends BaseCart {}",
    );
    let template = fixture.template(
        "templates/components/Cart.html.twig",
        r#"<button data-live-action-param="save">Save</button>"#,
        true,
    );

    let actions = fixture.components.live_actions("Cart").unwrap();
    assert_eq!(actions.len(), 1);
    assert_eq!(actions[0].name, "save");
    assert_eq!(actions[0].file, base);
    assert_eq!(actions[0].parameters.len(), 2);
    assert_eq!(actions[0].parameters[0].name, "itemId");
    assert_eq!(actions[0].parameters[0].php_name, "id");
    assert_eq!(actions[0].parameters[0].ty, "int");
    assert!(actions[0].parameters[0].live_arg);
    assert!(actions[0].parameters[1].optional);
    assert_eq!(
        fixture
            .components
            .live_actions_for_template(&template)
            .unwrap(),
        actions
    );

    let references = fixture
        .components
        .live_action_references("Cart", "save")
        .unwrap();
    assert_eq!(references.len(), 1);
    assert_eq!(references[0].file, template);
    assert_eq!(references[0].name, "save");
}

#[test]
fn index_resolves_inherited_repeatable_live_listeners_and_emissions() {
    let fixture = Fixture::new(false);
    let base = fixture.php_file(
        "src/Twig/Components/BaseCart.php",
        r"<?php
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
}",
    );
    let class = fixture.php_file(
        "src/Twig/Components/Cart.php",
        r"<?php
namespace App\Twig\Components;
use Symfony\UX\LiveComponent\Attribute\AsLiveComponent;
#[AsLiveComponent]
final class Cart extends BaseCart {
    public function save(): void {
        $this->emit('productAdded', ['productId' => 42]);
    }
}",
    );
    let template = fixture.template(
        "templates/components/Cart.html.twig",
        r#"<button
    data-action="live#emitSelf"
    data-live-event-param="productRestored"
    data-live-product-id-param="42"
></button>"#,
        true,
    );

    let listeners = fixture.components.live_listeners().unwrap();
    assert_eq!(listeners.len(), 2);
    assert_eq!(listeners[0].name, "productAdded");
    assert_eq!(listeners[0].file, base);
    assert_eq!(listeners[0].method, "refresh");
    assert_eq!(listeners[0].parameters.len(), 2);
    assert_eq!(listeners[0].parameters[0].name, "productId");
    assert_eq!(listeners[0].parameters[0].php_name, "id");
    assert!(listeners[0].parameters[1].optional);

    let references = fixture
        .components
        .live_event_references("productAdded")
        .unwrap();
    assert_eq!(references.len(), 1);
    assert_eq!(references[0].file, class);
    let references = fixture
        .components
        .live_event_references("productRestored")
        .unwrap();
    assert_eq!(references.len(), 1);
    assert_eq!(references[0].file, template);

    assert_eq!(
        fixture.components.live_event_names().unwrap(),
        vec!["productAdded".to_owned(), "productRestored".to_owned()]
    );
}

#[test]
fn index_restores_and_clears_candidate_records() {
    let dir = tempfile::tempdir().unwrap();
    let cache = dir.path();
    let path = cache
        .join("templates/page.html.twig")
        .to_string_lossy()
        .into_owned();
    let first = Index::new(cache, None).unwrap();
    first
        .index(&file(&path, "{{ component('Alert') }}"))
        .unwrap();
    first.close().unwrap();

    let restored = Index::new(cache, None).unwrap();
    assert_eq!(restored.usages("Alert").unwrap().len(), 1);
    restored.index(&file(&path, "plain text")).unwrap();
    assert!(restored.usages("Alert").unwrap().is_empty());
    // A later non-candidate file is no longer tracked and stays absent.
    restored.index(&file(&path, "plain text")).unwrap();
    assert!(restored.usages("Alert").unwrap().is_empty());

    restored
        .index(&file(&path, "{{ component('Alert') }}"))
        .unwrap();
    restored.removed_files(std::slice::from_ref(&path)).unwrap();
    assert!(restored.usages("Alert").unwrap().is_empty());
    restored
        .index(&file(&path, "{{ component('Alert') }}"))
        .unwrap();
    restored.clear().unwrap();
    assert!(restored.usages("Alert").unwrap().is_empty());
    restored.close().unwrap();
}

#[test]
fn index_discovers_modern_and_legacy_service_tags() {
    let fixture = Fixture::with(true, |root, cache, php| {
        let services = Arc::new(ServiceIndex::new(&root.to_string_lossy(), cache, None).unwrap());
        services.set_php_index(Some(php.clone()));
        let path = root
            .join("config/services.yaml")
            .to_string_lossy()
            .into_owned();
        services
            .index(&file(
                &path,
                r"services:
  App\Twig\Components\Alert:
    tags:
      - ux.twig_component
  App\Twig\Components\Legacy:
    tags:
      - twig.component
",
            ))
            .unwrap();
        Some(services)
    });
    let names = fixture.components.names().unwrap();
    assert!(names.contains(&"Alert".to_owned()));
    assert!(names.contains(&"Legacy".to_owned()));
}

#[test]
fn index_resolves_php_configured_component_defaults() {
    let fixture = Fixture::new(true);
    fixture.config(
        "config/packages/twig_component.php",
        r"<?php
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
];",
    );
    fixture.php_file(
        "src/Pizza/Components/Button/Primary.php",
        r"<?php
namespace App\Pizza\Components\Button;
use Symfony\UX\TwigComponent\Attribute\AsTwigComponent;
#[AsTwigComponent]
final class Primary {}
",
    );
    let template = fixture.template(
        "templates/ux-components/Alert.html.twig",
        "<div></div>",
        false,
    );

    let components = fixture.components.find("Pizza:Button:Primary").unwrap();
    assert_eq!(components.len(), 1);
    assert_eq!(
        components[0].template,
        "components/pizza/Button/Primary.html.twig"
    );
    let alerts = fixture.components.find("Alert").unwrap();
    assert_eq!(alerts.len(), 1);
    assert_eq!(alerts[0].file, template);
}

fn compiled_container(components: &str, class_map: &str) -> String {
    format!(
        r#"<container><services>
<service id="ux.twig_component.component_factory">
  <argument/><argument/><argument/><argument/>
  <argument type="collection">{components}</argument>
  <argument type="collection">{class_map}</argument>
</service>
</services></container>"#
    )
}

fn assert_compiled(component: &Component, class: &str, template: &str) {
    assert_eq!(component.class, class);
    assert_eq!(component.template, template);
    assert_eq!(component.source, SourceKind::CompiledContainer);
}

#[test]
fn index_uses_compiled_container_twig_components() {
    let mut services_handle = None;
    let fixture = Fixture::with(true, |root, cache, _| {
        let container_dir = root.join("var/cache/dev-test");
        std::fs::create_dir_all(&container_dir).unwrap();
        std::fs::write(
            container_dir.join("Shopware_Core_KernelDevDebugContainer.xml"),
            compiled_container(
                r#"<argument key="CompiledOnly" type="collection">
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
</argument>"#,
                r#"<argument key="App\Twig\Components\CompiledOnly">CompiledOnly</argument>
<argument key="App\Twig\Components\Override">Override</argument>
<argument key="App\Twig\Components\Dynamic">Dynamic</argument>"#,
            ),
        )
        .unwrap();
        let services = Arc::new(ServiceIndex::new(&root.to_string_lossy(), cache, None).unwrap());
        services.reload_compiled_container().unwrap();
        services_handle = Some(services.clone());
        Some(services)
    });
    let services = services_handle.unwrap();
    fixture.config(
        "config/packages/twig_component.yaml",
        r"twig_component:
  defaults:
    App\Twig\Components\:
      template_directory: components/from-yaml
",
    );
    fixture.php_file(
        "src/Twig/Components/Components.php",
        r"<?php
namespace App\Twig\Components;
use Symfony\UX\TwigComponent\Attribute\AsTwigComponent;
final class CompiledOnly {
    public string $title = 'hello';
}
#[AsTwigComponent(name: 'Override')]
final class Override {}
final class Dynamic {}
",
    );
    let template = fixture.template("templates/compiled/Only.html.twig", "{{ title }}", false);

    let compiled_only = fixture.components.find("CompiledOnly").unwrap();
    assert_eq!(compiled_only.len(), 1);
    assert_compiled(
        &compiled_only[0],
        "App\\Twig\\Components\\CompiledOnly",
        "compiled/Only.html.twig",
    );
    let props = fixture.components.props("CompiledOnly").unwrap();
    assert_eq!(props.len(), 1);
    assert_eq!(props[0].name, "title");

    let overridden = fixture.components.find("Override").unwrap();
    assert_eq!(overridden.len(), 1);
    assert_compiled(
        &overridden[0],
        "App\\Twig\\Components\\Override",
        "compiled/Override.html.twig",
    );

    let dynamic = fixture.components.find("Dynamic").unwrap();
    assert_eq!(dynamic.len(), 1);
    assert!(dynamic[0].template.is_empty());
    assert_eq!(dynamic[0].template_from_method, "getTemplate");
    assert_eq!(dynamic[0].source, SourceKind::CompiledContainer);

    let reverse = fixture
        .components
        .components_for_template(&template)
        .unwrap();
    assert_eq!(reverse.len(), 1);
    assert_eq!(reverse[0].name, "CompiledOnly");

    std::fs::write(
        fixture
            .root
            .join("var/cache/dev-test/Shopware_Core_KernelDevDebugContainer.xml"),
        compiled_container(
            r#"<argument key="Reloaded" type="collection">
  <argument key="class">App\Twig\Components\Reloaded</argument>
  <argument key="template">compiled/Reloaded.html.twig</argument>
</argument>"#,
            r#"<argument key="App\Twig\Components\Reloaded">Reloaded</argument>"#,
        ),
    )
    .unwrap();
    services.reload_compiled_container().unwrap();
    let reloaded = fixture.components.find("Reloaded").unwrap();
    assert_eq!(reloaded.len(), 1);
    assert_eq!(reloaded[0].template, "compiled/Reloaded.html.twig");
    let _ = services.close();
}

#[test]
fn component_workspace_symbols_describe_declarations() {
    let path = "/project/src/Twig/Components/Alert.php";
    let source = r"<?php
namespace App\Twig\Components;
use Symfony\UX\LiveComponent\Attribute\AsLiveComponent;
#[AsLiveComponent('Alert')]
final class Alert {}";
    let parsed = file(path, source);
    let record = Record {
        declarations: declarations_in_php(path, &php(source)),
        ..Record::default()
    };
    let symbols = crate::index::component_workspace_symbols(&parsed, &record);
    assert_eq!(symbols.len(), 1);
    assert_eq!(symbols[0].name, "Alert");
    assert_eq!(
        symbols[0].container_name,
        "Live component · App\\Twig\\Components\\Alert"
    );
    assert_eq!(symbols[0].domain, "twig.component");
    assert_eq!(symbols[0].range.start.line, 3);
}
