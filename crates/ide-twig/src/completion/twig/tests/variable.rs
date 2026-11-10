use swlsp_lsp_protocol::CompletionItemKind;
use swlsp_parser_twig::query as twigquery;
use swlsp_parser_twig::syntax::TWIG_ACCESSOR;
use tempfile::TempDir;

use super::*;
use crate::completion::twig::TwigCompletionProvider;

pub(super) fn variable_fixture() -> (TwigCompletionProvider, Vec<TempDir>) {
    let (php_dir, twig_dir) = (tmp(), tmp());
    let php = php_index(&php_dir);
    index(
        &php,
        "/project/src/ProductController.php",
        r#"<?php
namespace App;

class Category {
    public function getLabel(): string {}
}

class Product {
    public const KIND = 'product';
    public string $name;
    public function getTitle(): string {}
    public function getCategory(): Category {}
    public function calculate(): int {}
}

class ProductController {
    public function show(Product $product) {
        return $this->render('product/show.html.twig', [
            'product' => $product,
        ]);
    }
}"#,
    );
    let twig = twig_index(&twig_dir);
    (
        TwigCompletionProvider::new("/project", Some(twig), None, Some(php)),
        vec![php_dir, twig_dir],
    )
}

#[test]
fn twig_controller_variable_completion() {
    let (provider, _dirs) = variable_fixture();
    let source = "{{ pro }}";
    let request = twig_completion_at(
        "file:///project/templates/product/show.html.twig",
        source,
        source.find("pro").unwrap() + 3,
    );
    let items = complete(&provider, &request);
    let product = require_completion(&items, "product");
    assert_eq!(product.kind, Some(CompletionItemKind::VARIABLE));
    assert_eq!(product.detail, "App\\Product");
    assert!(documentation(&product).contains("ProductController.php"));
}

#[test]
fn twig_controller_variable_member_completion() {
    let (provider, _dirs) = variable_fixture();
    let source = "{{ product. }}";
    let request = twig_completion_at(
        "file:///project/templates/product/show.html.twig",
        source,
        source.find('.').unwrap() + 1,
    );
    let items = complete(&provider, &request);
    let name = require_completion(&items, "name");
    assert_eq!(name.kind, Some(CompletionItemKind::PROPERTY));
    let title = require_completion(&items, "title");
    assert!(title.detail.contains("getTitle()"));
    require_completion(&items, "category");
    require_completion(&items, "calculate");
    let kind = require_completion(&items, "KIND");
    assert_eq!(kind.kind, Some(CompletionItemKind::CONSTANT));

    let nested = "{{ product.category. }}";
    let request = twig_completion_at(
        "file:///project/templates/product/show.html.twig",
        nested,
        nested.rfind('.').unwrap() + 1,
    );
    require_completion(&complete(&provider, &request), "label");
}

#[test]
fn twig_controller_variables_do_not_leak_across_templates() {
    let (provider, _dirs) = variable_fixture();
    let source = "{{ pro }}";
    let request = twig_completion_at(
        "file:///project/templates/product/edit.html.twig",
        source,
        source.find("pro").unwrap() + 3,
    );
    assert!(!labels(&complete(&provider, &request)).contains(&"product".to_string()));
}

#[test]
fn twig3_loop_variable_completion_is_scoped() {
    let (provider, _dirs) = variable_fixture();
    assert!(swlsp_twig::loop_context_type(provider.php_index.as_ref()).is_none());
    let tests = [
        (
            "loop body",
            "{% for entry in entries %}\n{{ loop. }}\n{% endfor %}",
            true,
        ),
        (
            "nested else keeps outer loop",
            "{% for entry in entries %}\n{% for child in entry.children %}{% else %}{{ loop. }}{% endfor %}\n{% endfor %}",
            true,
        ),
        (
            "for else",
            "{% for entry in entries %}{% else %}\n{{ loop. }}\n{% endfor %}",
            false,
        ),
        ("outside loop", "{{ loop. }}", false),
        (
            "unrelated receiver",
            "{% for entry in entries %}\n{{ entry. }}\n{% endfor %}",
            false,
        ),
    ];
    for (name, source, found) in tests {
        let offset = source.find(". }}").unwrap() + 1;
        let request =
            twig_completion_at("file:///project/templates/loop.html.twig", source, offset);
        let accessor =
            twigquery::closest_node_of_kind(request.node.as_ref().unwrap(), &[TWIG_ACCESSOR])
                .unwrap();
        assert_eq!(
            found,
            swlsp_twig::loop_accessor_in_scope(&accessor),
            "{name}"
        );
        let items = complete(&provider, &request);
        let names = labels(&items);
        if !found {
            assert!(!names.contains(&"index".to_string()), "{name}");
            continue;
        }
        for label in [
            "index",
            "index0",
            "revindex",
            "revindex0",
            "first",
            "last",
            "length",
            "parent",
        ] {
            let item = require_completion(&items, label);
            assert_eq!(item.kind, Some(CompletionItemKind::PROPERTY), "{name}");
        }
        assert!(!names.contains(&"previous".to_string()), "{name}");
    }
}

#[test]
fn twig4_loop_variable_completion_uses_runtime_class() {
    let (php_dir, twig_dir) = (tmp(), tmp());
    let php = php_index(&php_dir);
    index(
        &php,
        "/project/vendor/twig/twig/src/Runtime/LoopContext.php",
        r#"<?php
namespace Twig\Runtime;
final class LoopContext {
    public function getIndex(): int {}
    public function getIndex0(): int {}
    public function isFirst(): bool {}
    public function isLast(): bool {}
    public function getPrevious(): mixed {}
    public function getNext(): mixed {}
    public function hasChanged(mixed $value): bool {}
    public function cycle(mixed ...$values): mixed {}
    public function getDepth(): int {}
    public function getDepth0(): int {}
    public function getFixtureOnly(): string {}
    public function setIgnored(mixed $value): void {}
    public function __invoke(iterable $iterator): iterable {}
    private function getPrivateValue(): string {}
}"#,
    );
    let provider =
        TwigCompletionProvider::new("/project", Some(twig_index(&twig_dir)), None, Some(php));
    let source = "{% for entry in entries %}{{ loop. }}{% endfor %}";
    let request = twig_completion_at(
        "file:///project/templates/loop.html.twig",
        source,
        source.find(". }}").unwrap() + 1,
    );
    let items = complete(&provider, &request);
    for name in [
        "index",
        "index0",
        "first",
        "last",
        "previous",
        "next",
        "changed",
        "cycle",
        "depth",
        "depth0",
        "fixtureOnly",
    ] {
        require_completion(&items, name);
    }
    let names = labels(&items);
    for absent in ["setIgnored", "__invoke", "privateValue", "revindex"] {
        assert!(!names.contains(&absent.to_string()), "{absent}");
    }
}

#[test]
fn twig_global_variable_and_member_completion() {
    let cache = tmp();
    let php = php_index(&cache);
    let twig = twig_index(&cache);
    twig.set_dependencies(Some(php.clone()), None);
    let source = r#"<?php
namespace App;
use Twig\Extension\AbstractExtension;
class Clock {
    public string $timezone;
    public function getNow(): string {}
}
class StorefrontExtension extends AbstractExtension {
    public function getGlobals(): array {
        return ['clock' => new Clock()];
    }
}"#;
    let path = "/project/src/StorefrontExtension.php";
    index(&php, path, source);
    index(twig.as_ref(), path, source);
    let provider = TwigCompletionProvider::new("/project", Some(twig), None, Some(php.clone()));

    let text = "{{ clo }}";
    let request = twig_completion_at(
        "file:///project/templates/page.html.twig",
        text,
        text.find("clo").unwrap() + 3,
    );
    let clock = require_completion(&complete(&provider, &request), "clock");
    assert_eq!(clock.detail, "App\\Clock");
    assert!(documentation(&clock).contains("Twig extension"));

    let text = "{{ clock. }}";
    let member_request = twig_completion_at(
        "file:///project/templates/page.html.twig",
        text,
        text.find('.').unwrap() + 1,
    );
    let members = complete(&provider, &member_request);
    require_completion(&members, "timezone");
    require_completion(&members, "now");

    index(
        &php,
        "/project/src/PageController.php",
        r#"<?php
namespace App;
class LocalClock {
    public string $localOnly;
}
class PageController {
    public function page() {
        return $this->render('page.html.twig', [
            'clock' => new LocalClock(),
        ]);
    }
}"#,
    );
    let local = complete(&provider, &member_request);
    require_completion(&local, "localOnly");
    assert!(!labels(&local).contains(&"timezone".to_string()));
}
