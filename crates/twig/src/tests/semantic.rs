use std::collections::BTreeSet;

use swlsp_indexer::{Indexer, ParsedFile};
use swlsp_parser_twig::query as twigquery;
use swlsp_parser_twig::syntax::{TWIG_LITERAL_NAME, TWIG_LITERAL_STRING};
use swlsp_php_semantic::{Flags, SymbolKind};

use super::indexer::{index_php, php_index};
use crate::*;

fn file(path: &str, source: &str) -> ParsedFile {
    ParsedFile::new(path, source.as_bytes().to_vec())
}

#[test]
fn php_access_resolver_handles_variables_functions_and_filters() {
    let cache = tempfile::tempdir().unwrap();
    let php = php_index(cache.path());
    let twig = TwigIndexer::new(cache.path(), None).unwrap();
    twig.set_dependencies(Some(php.clone()), None);
    let path = "/project/src/Twig/StringExtension.php";
    let source = r"<?php
namespace Foo {
    class Bar {
        /** @deprecated */
        public string $deprecatedProperty;

        #[\Deprecated]
        public string $deprecatedAttributeProperty;

        public function getNext(): Bar { return new Bar(); }

        /** @deprecated */
        public function getDeprecated(): static { return $this; }
    }
}
namespace Twig\Extra\String {
    class StringExtension extends \Twig_Extension {
        public function getFilters(): array {
            return [new \Twig_SimpleFilter('u', [$this, 'createBar'])];
        }
        public function getFunctions(): array {
            return [new \Twig_SimpleFunction('ustring', [$this, 'createBar'])];
        }
        public function createBar(?string $text = null): \Foo\Bar {
            return new \Foo\Bar();
        }
    }
}";
    index_php(&php, path, source);
    twig.index(&file(path, source)).unwrap();
    assert_eq!(twig.get_twig_function("ustring").unwrap().len(), 1);
    assert_eq!(twig.get_twig_filter("u").unwrap().len(), 1);

    let root = swlsp_parser_twig::parse(
        r"{# @var bar \Foo\Bar #}
{{ bar.next.deprecated }}
{{ bar.deprecatedProperty }}
{{ bar.deprecatedAttributeProperty }}
{{ ustring('value').deprecated }}
{{ 'value'|u.deprecated }}
{{ bar.deprecated.deprecated }}
",
    )
    .root();
    let resolver = PhpAccessResolver::new(Some(&php), Some(&twig));
    assert_eq!(
        resolver.twig_function_type("ustring").to_string(),
        r"Foo\Bar"
    );
    assert_eq!(resolver.twig_filter_type("u").to_string(), r"Foo\Bar");
    for (name, expected) in [
        ("deprecated", 5),
        ("deprecatedProperty", 1),
        ("deprecatedAttributeProperty", 1),
    ] {
        let mut resolved = 0;
        for node in twigquery::nodes(&root, &[TWIG_LITERAL_NAME]) {
            if node.text() != name {
                continue;
            }
            let Some(resolution) =
                resolver.resolve_name("/project/templates/page.html.twig", &root, &node)
            else {
                continue;
            };
            resolved += 1;
            assert!(
                resolution.members[0].symbol.flags.has(Flags::DEPRECATED),
                "{name}"
            );
        }
        assert_eq!(resolved, expected, "{name}");
    }
    twig.close().unwrap();
    php.close().unwrap();
}

#[test]
fn twig_type_annotations_support_both_orders() {
    let root = swlsp_parser_twig::parse(
        r"{# @var product \App\Product #}
{# @var \App\Category $category #}
{# @var \App\Customer customer #}
{% types {
    order: '\App\Order',
    products?: '\App\Product[]',
} %}",
    )
    .root();
    let annotations = twig_type_annotations(&root);
    assert_eq!(annotations["product"].to_string(), r"App\Product");
    assert_eq!(annotations["category"].to_string(), r"App\Category");
    assert_eq!(annotations["customer"].to_string(), r"App\Customer");
    assert_eq!(annotations["order"].to_string(), r"App\Order");
    assert_eq!(annotations["products"].to_string(), r"list<App\Product>");
}

#[test]
fn php_access_resolver_scopes_prepared_annotations_to_document() {
    let first = swlsp_parser_twig::parse(r"{# @var value \App\First #}").root();
    let second = swlsp_parser_twig::parse(r"{# @var value \App\Second #}").root();
    let resolver = PhpAccessResolver::new(None, None).for_document(&first);
    assert_eq!(
        resolver.root_name_type("", &first, "value").to_string(),
        r"App\First"
    );
    assert_eq!(
        resolver.root_name_type("", &second, "value").to_string(),
        r"App\Second"
    );
}

#[test]
fn php_access_resolver_types_twig_for_variables_from_types_tag() {
    let cache = tempfile::tempdir().unwrap();
    let php = php_index(cache.path());
    index_php(
        &php,
        "/project/src/Product.php",
        "<?php\nnamespace App;\nclass Product {\n    public string $name;\n}",
    );
    let root = swlsp_parser_twig::parse(
        r"{% types {
    products: 'App\\Product[]',
} %}
{% for key, product in products %}
{{ product.name }}
{% else %}
{{ product.name }}
{% endfor %}",
    )
    .root();
    let resolver = PhpAccessResolver::new(Some(&php), None);
    let (mut body, mut other) = (0, 0);
    for node in twigquery::nodes(&root, &[TWIG_LITERAL_NAME]) {
        if node.text().to_string().trim() != "name" {
            continue;
        }
        if resolver
            .resolve_name("/project/templates/products.html.twig", &root, &node)
            .is_some()
        {
            body += 1;
        } else {
            other += 1;
        }
    }
    assert_eq!((body, other), (1, 1));
    php.close().unwrap();
}

#[test]
fn php_access_resolver_uses_twig4_loop_context_only_in_for_body() {
    let cache = tempfile::tempdir().unwrap();
    let php = php_index(cache.path());
    index_php(
        &php,
        "/project/vendor/twig/twig/src/Runtime/LoopContext.php",
        "<?php\nnamespace Twig\\Runtime;\nfinal class LoopContext {\n    public function getPrevious(): mixed {}\n}",
    );
    let root = swlsp_parser_twig::parse(
        "{% for entry in entries %}\n{{ loop.previous }}\n{% else %}\n{{ loop.previous }}\n{% endfor %}\n{{ loop.previous }}",
    )
    .root();
    let resolver = PhpAccessResolver::new(Some(&php), None);
    let mut resolved = 0;
    for node in twigquery::nodes(&root, &[TWIG_LITERAL_NAME]) {
        if node.text() != "previous" {
            continue;
        }
        let Some(resolution) =
            resolver.resolve_name("/project/templates/loop.html.twig", &root, &node)
        else {
            continue;
        };
        resolved += 1;
        assert_eq!(resolution.receiver.to_string(), TWIG_LOOP_CONTEXT_CLASS);
        assert_eq!(resolution.members.len(), 1);
        assert_eq!(&*resolution.members[0].symbol.name, "getPrevious");
    }
    assert_eq!(resolved, 1);
    php.close().unwrap();
}

#[test]
fn php_usage_references_resolve_members_and_classes() {
    let cache = tempfile::tempdir().unwrap();
    let php = php_index(cache.path());
    index_php(
        &php,
        "/tmp-project/Bar.php",
        "<?php
namespace Foo;
class Bar {
    public string $RestVar;
    public function __construct(public float $primaryValue) {}
    public function getFoo(): string { return ''; }
    public const CLUBS = 'clubs';
}
enum CardSuite { case HEARTS; }
",
    );
    let source = r"{# @var bar \Foo\Bar #}
{{ bar.RestVar }}
{{ bar.primaryValue }}
{{ bar.foo }}
{{ bar.getFoo() }}
{{ bar.CLUBS }}
{{ enum('Foo\\CardSuite') }}
{# @var \Foo\Bar $other #}
{# @var \Foo\Bar third #}";
    let root = swlsp_parser_twig::parse(source).root();
    let references = php_usage_references_in_document(
        "/project/templates/usage.html.twig",
        &root,
        &PhpAccessResolver::new(Some(&php), None),
    );
    let accesses = |kind: SymbolKind| -> BTreeSet<String> {
        references
            .iter()
            .filter(|r| !r.member.is_empty() && r.kind == kind)
            .map(|r| r.access.clone())
            .collect()
    };
    assert_eq!(
        accesses(SymbolKind::Property),
        BTreeSet::from(["RestVar".into(), "primaryValue".into()])
    );
    assert_eq!(
        accesses(SymbolKind::Method),
        BTreeSet::from(["foo".into(), "getFoo".into()])
    );
    assert_eq!(
        accesses(SymbolKind::ClassConstant),
        BTreeSet::from(["CLUBS".into()])
    );
    let mut classes: Vec<&str> = Vec::new();
    for reference in references.iter().filter(|r| r.member.is_empty()) {
        assert_eq!(reference.access, &source[reference.range]);
        classes.push(&reference.class);
    }
    classes.sort_unstable();
    assert_eq!(
        classes,
        [r"Foo\Bar", r"Foo\Bar", r"Foo\Bar", r"Foo\CardSuite"]
    );
    php.close().unwrap();
}

#[test]
fn php_usage_references_use_types_tag_loop_declarations() {
    let cache = tempfile::tempdir().unwrap();
    let php = php_index(cache.path());
    index_php(
        &php,
        "/project/src/Product.php",
        "<?php\nnamespace App;\nclass Product {\n    public string $name;\n}",
    );
    let source = "{% types { products: 'App\\\\Product[]' } %}\n{% for product in products %}{{ product.name }}{% endfor %}";
    let references = php_usage_references_in_document(
        "/project/templates/product.html.twig",
        &swlsp_parser_twig::parse(source).root(),
        &PhpAccessResolver::new(Some(&php), None),
    );
    let class = references
        .iter()
        .find(|r| r.member.is_empty() && r.class == r"App\Product")
        .expect("class");
    assert_eq!(class.access, r"App\\Product");
    assert!(
        references
            .iter()
            .any(|r| r.kind == SymbolKind::Property && r.member == "name")
    );
    php.close().unwrap();
}

#[test]
fn php_usage_target_for_symbol_uses_declaring_class() {
    let cache = tempfile::tempdir().unwrap();
    let php = php_index(cache.path());
    index_php(
        &php,
        "/project/Classes.php",
        "<?php\nnamespace App;\nclass ParentType { public function getName(): string { return ''; } }\nclass ChildType extends ParentType {}\n",
    );
    let methods = php.find_methods(r"App\ChildType", "getName");
    assert_eq!(methods.len(), 1);
    let target =
        php_usage_target_for_symbol(&php.semantic_snapshot(), &methods[0]).expect("target");
    assert_eq!(target.class, r"App\ParentType");
    assert_eq!(target.member, "getName");
    assert_eq!(target.kind, SymbolKind::Method);
    php.close().unwrap();
}

#[test]
fn extension_usages_link_twig_functions_and_filters_to_methods() {
    let cache = tempfile::tempdir().unwrap();
    let twig_cache = tempfile::tempdir().unwrap();
    let php = php_index(cache.path());
    let path = "/project/ext/AppExtension.php";
    let source = r"<?php
namespace App\Twig;
use Twig\Attribute\AsTwigFilter;
use Twig\Attribute\AsTwigFunction;

class AppExtension {
    #[AsTwigFunction('product_number_function')]
    public function formatProductNumberFunction(string $number): string {
        return $number;
    }

    #[AsTwigFilter('product_number_filter')]
    public function formatProductNumberFilter(string $number): string {
        return $number;
    }
}";
    index_php(&php, path, source);
    let twig = TwigIndexer::new(twig_cache.path(), None).unwrap();
    twig.set_dependencies(Some(php.clone()), None);
    twig.index(&file(path, source)).unwrap();
    twig.index(&file(
        "/project/templates/extensions.html.twig",
        "{{ product_number_function('123') }}\n{{ value|product_number_filter }}",
    ))
    .unwrap();
    for method in ["formatProductNumberFunction", "formatProductNumberFilter"] {
        let methods = php.find_methods(r"App\Twig\AppExtension", method);
        assert_eq!(methods.len(), 1);
        assert_eq!(
            twig.get_extension_usages_for_php_symbol(&php, &methods[0])
                .unwrap()
                .len(),
            1,
            "{method}"
        );
    }
    twig.close().unwrap();
    php.close().unwrap();
}

#[test]
fn extension_usages_in_document_find_exact_functions_filters_and_tests() {
    let source = "{{ product_number_function('123') }}
{{ value|product_number_filter }}
{% apply product_number_filter %}value{% endapply %}
{{ forms.input('email') }}
{{ value|product_number_filter(argument) }}
{% if value is product_number_test %}
{% elseif value is product_number_test('strict') %}
{% endif %}";
    let usages = extension_usages_in_document(
        "/project/template.html.twig",
        &swlsp_parser_twig::parse(source).root(),
    );
    let of = |kind: ExtensionUsageKind| -> Vec<&str> {
        usages
            .iter()
            .filter(|u| u.kind == kind)
            .map(|u| u.name.as_str())
            .collect()
    };
    for usage in &usages {
        assert_eq!(usage.name, &source[usage.range]);
    }
    assert_eq!(
        of(ExtensionUsageKind::Function),
        ["product_number_function"]
    );
    assert_eq!(of(ExtensionUsageKind::Filter), ["product_number_filter"; 3]);
    assert_eq!(of(ExtensionUsageKind::Test), ["product_number_test"; 2]);
}

#[test]
fn extension_usage_at_matches_only_name_range() {
    let source = "{{ form_start(value) }}";
    let root = swlsp_parser_twig::parse(source).root();
    let usage = extension_usage_at(
        "/project/template.html.twig",
        &root,
        "{{ form_".len() as u32,
    )
    .expect("usage");
    assert_eq!(usage.kind, ExtensionUsageKind::Function);
    assert_eq!(usage.name, "form_start");
    assert!(
        extension_usage_at(
            "/project/template.html.twig",
            &root,
            "{{ form_start(".len() as u32
        )
        .is_none()
    );
}

#[test]
fn constant_references_resolve_global_class_and_object_forms() {
    let source = r"{# @var suite \BugDemo\CardSuite #}
{{ constant('App\\Cards::CLUBS') }}
{{ constant('\\BugDemo\\NAMESPACED_CONST') }}
{{ constant('SPADES', suite) }}
{{ constant('IGNORED', factory()) }}
{{ constant(dynamic) }}";
    let root = swlsp_parser_twig::parse(source).root();
    let resolver = PhpAccessResolver::new(None, None);
    let path = "/project/templates/cards.html.twig";
    let references = constant_references_in_document(path, &root, &resolver);
    assert_eq!(references.len(), 3);
    assert_eq!(
        (references[0].class.as_str(), references[0].name.as_str()),
        (r"App\Cards", "CLUBS")
    );
    assert_eq!(
        (references[1].class.as_str(), references[1].name.as_str()),
        ("", r"BugDemo\NAMESPACED_CONST")
    );
    assert_eq!(
        (references[2].class.as_str(), references[2].name.as_str()),
        (r"BugDemo\CardSuite", "SPADES")
    );
    assert!(
        references
            .iter()
            .all(|reference| !source[reference.range].is_empty())
    );
    let literals = twigquery::nodes(&root, &[TWIG_LITERAL_STRING]);
    let at = constant_references_at(path, &root, &literals[2], &resolver);
    assert_eq!(at.len(), 1);
    assert_eq!(at[0].class, r"BugDemo\CardSuite");
    let context =
        constant_completion_context_at(path, &root, &literals[2], &resolver).expect("context");
    assert!(context.object_argument);
    assert_eq!(context.receiver_classes, [r"BugDemo\CardSuite"]);
}

#[test]
fn constant_reference_keys_separate_global_and_class_constants() {
    let global = ConstantReference {
        name: "VALUE".into(),
        ..ConstantReference::default()
    };
    let class = ConstantReference {
        class: r"App\Config".into(),
        name: "VALUE".into(),
        ..ConstantReference::default()
    };
    assert_ne!(
        constant_reference_key(&global),
        constant_reference_key(&class)
    );
    let trimmed = ConstantReference {
        class: r"\App\Config\".into(),
        name: "VALUE".into(),
        ..ConstantReference::default()
    };
    assert_eq!(
        constant_reference_key(&trimmed),
        "class\0app\\config\0value"
    );
}
