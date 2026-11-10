use std::sync::Arc;

use swlsp_indexer::{Indexer, ParsedFile};
use swlsp_lsp::{CompletionProvider, Context, TextDocument};
use swlsp_lsp_protocol as protocol;
use swlsp_php::PhpIndex;
use tempfile::TempDir;

use super::{additional_edit, edit, labels, node_at, p, require_completion};
use crate::completion::PhpAttributeCompletionProvider;

const VENDOR: &str = r#"<?php
namespace Symfony\Component\Routing\Attribute;
class Route {}
namespace Symfony\Component\Security\Http\Attribute;
class IsGranted {}
namespace Symfony\Component\HttpKernel\Attribute;
class Cache {}
class AsController {}
namespace Symfony\Component\Console\Attribute;
class AsCommand {}
namespace Symfony\Component\Console\Input;
interface InputInterface {}
namespace Symfony\Component\Console\Output;
interface OutputInterface {}
namespace Symfony\Component\Console\Command;
class Command {}
namespace Symfony\Bundle\FrameworkBundle\Controller;
abstract class AbstractController {}
namespace Twig\Attribute;
class AsTwigFilter {}
class AsTwigFunction {}
class AsTwigTest {}
namespace Twig\Extension;
interface ExtensionInterface {}
abstract class AbstractExtension implements ExtensionInterface {}
namespace Symfony\UX\TwigComponent\Attribute;
class AsTwigComponent {}
namespace Doctrine\ORM\Mapping;
class Column {}
class Id {}
class GeneratedValue {}
class OneToMany {}
class OneToOne {}
class ManyToOne {}
class ManyToMany {}
class JoinColumn {}
class Entity {}
class Table {}
class UniqueConstraint {}
class Index {}
class Embeddable {}
class HasLifecycleCallbacks {}
class PostLoad {}
class PostPersist {}
class PostRemove {}
class PostUpdate {}
class PrePersist {}
class PreRemove {}
class PreUpdate {}
"#;

struct Fixture {
    root: TempDir,
    _cache: TempDir,
    php: PhpIndex,
    provider: PhpAttributeCompletionProvider,
}

fn fixture() -> Fixture {
    let root = tempfile::tempdir().unwrap();
    let cache = tempfile::tempdir().unwrap();
    let php = PhpIndex::open(cache.path(), None).unwrap();
    php.index(&ParsedFile::new(
        p(&root.path().join("vendor").join("attributes.php")),
        VENDOR.as_bytes().to_vec(),
    ))
    .unwrap();
    let provider = PhpAttributeCompletionProvider::new(Some(php.clone()));
    Fixture {
        root,
        _cache: cache,
        php,
        provider,
    }
}

fn completions(
    provider: &PhpAttributeCompletionProvider,
    root: &TempDir,
    source: &str,
    offset: usize,
) -> Vec<protocol::CompletionItem> {
    let path = p(&root.path().join("src").join("Subject.php"));
    let document = Arc::new(TextDocument::new(swlsp_uriutil::file_uri(&path), source, 1));
    let mut node_offset = offset.min(source.len() - 1);
    node_offset = node_offset.saturating_sub(1);
    let node = node_at(&document, node_offset as u32);
    let request =
        crate::completion::test_support::completion_request(document, node, offset as u32);
    provider.get_completions(&Context::background(), &request)
}

fn sorted(mut values: Vec<String>) -> Vec<String> {
    values.sort();
    values
}

fn range_text(source: &str, range: protocol::Range) -> String {
    let document = TextDocument::new("file:///range.php", source, 1);
    let start = document
        .line_index()
        .offset_utf16(range.start.line, range.start.character) as usize;
    let end = document
        .line_index()
        .offset_utf16(range.end.line, range.end.character) as usize;
    source[start..end].to_string()
}

#[test]
fn php_attribute_completion_uses_controller_member_scope() {
    let f = fixture();
    let source = r#"<?php
namespace App\Controller;

class ProductController
{
    #[Rou]
    public function detail(): void {}
}"#;
    let items = completions(
        &f.provider,
        &f.root,
        source,
        source.find("Rou").unwrap() + 3,
    );
    assert_eq!(sorted(labels(&items)), ["Cache", "IsGranted", "Route"]);
    let route = require_completion(&items, "Route");
    assert_eq!(edit(&route).new_text, "Route('${1}')$0");
    assert_eq!(
        route.insert_text_format,
        Some(protocol::InsertTextFormat::SNIPPET)
    );
    assert_eq!(route.additional_text_edits.len(), 1);
    assert!(
        additional_edit(&route, 0)
            .new_text
            .contains("use Symfony\\Component\\Routing\\Attribute\\Route;")
    );
}

#[test]
fn php_attribute_completion_supports_lone_hash_at_class_scope() {
    let f = fixture();
    let source = r#"<?php
namespace App\Controller;

#
final class ProductController {}
"#;
    let offset = source.find('#').unwrap() + 1;
    let items = completions(&f.provider, &f.root, source, offset);
    assert_eq!(
        sorted(labels(&items)),
        ["AsController", "IsGranted", "Route"]
    );
    let as_controller = require_completion(&items, "AsController");
    let edit = edit(&as_controller);
    assert_eq!(edit.new_text, "#[AsController]");
    assert_eq!(range_text(source, edit.range), "#");
}

#[test]
fn php_attribute_completion_recognizes_twig_and_command_semantics() {
    let f = fixture();
    let twig_source = r#"<?php
namespace App\Twig;
use Twig\Extension\AbstractExtension;
class FormatExtension extends AbstractExtension {
    #[AsT]
    public function money(): string {}
}"#;
    let twig_items = completions(
        &f.provider,
        &f.root,
        twig_source,
        twig_source.find("AsT").unwrap() + 3,
    );
    assert_eq!(
        sorted(labels(&twig_items)),
        ["AsTwigFilter", "AsTwigFunction", "AsTwigTest"]
    );

    let command_source = r#"<?php
namespace App\Endpoint;
use Symfony\Component\Console\Input\InputInterface;
class ImportProducts {
    public function __invoke(InputInterface $input): void {}
}"#;
    let hash_offset = command_source.find("class ImportProducts").unwrap();
    let command_source = format!(
        "{}#\n{}",
        &command_source[..hash_offset],
        &command_source[hash_offset..]
    );
    let items = completions(&f.provider, &f.root, &command_source, hash_offset + 1);
    assert_eq!(labels(&items), ["AsCommand"]);
}

#[test]
fn php_attribute_completion_reuses_aliases_and_avoids_short_name_conflicts() {
    let f = fixture();
    for (import_line, expected) in [
        (
            "use Symfony\\Component\\Routing\\Attribute\\Route as WebRoute;",
            "WebRoute('${1}')$0",
        ),
        (
            "use App\\Metadata\\Route;",
            "\\Symfony\\Component\\Routing\\Attribute\\Route('${1}')$0",
        ),
    ] {
        let source = format!(
            "<?php\nnamespace App\\Controller;\n{import_line}\nclass ProductController {{\n    #[Rou]\n    public function detail(): void {{}}\n}}"
        );
        let items = completions(
            &f.provider,
            &f.root,
            &source,
            source.rfind("Rou").unwrap() + 3,
        );
        let route = require_completion(&items, "Route");
        assert_eq!(edit(&route).new_text, expected);
        assert!(route.additional_text_edits.is_empty());
    }
}

#[test]
fn php_attribute_completion_offers_class_specific_mappings() {
    let f = fixture();
    let component = r#"<?php
namespace App\Twig\Components;
#
class Card {}
"#;
    let items = completions(
        &f.provider,
        &f.root,
        component,
        component.find('#').unwrap() + 1,
    );
    assert_eq!(labels(&items), ["AsTwigComponent"]);

    let entity = r#"<?php
namespace App\Entity;
use Doctrine\ORM\Mapping as ORM;
#[Ent]
class Product {}
"#;
    let items = completions(
        &f.provider,
        &f.root,
        entity,
        entity.rfind("Ent").unwrap() + 3,
    );
    assert!(items.is_empty());
}

#[test]
fn php_attribute_completion_rejects_invalid_scopes_and_missing_classes() {
    let f = fixture();
    for source in [
        "<?php\nclass Ordinary {\n    #[Rou]\n    public function run(): void {}\n}",
        "<?php\nnamespace App\\Controller;\nclass ProductController {\n    #[Rou]\n    private function helper(): void {}\n}",
        "<?php\nnamespace App\\Controller;\nclass ProductController {\n    #[Rou]\n    public static function helper(): void {}\n}",
        "<?php\nnamespace App\\Controller;\nclass ProductController {\n    public function detail(#[Rou] string $value): void {}\n}",
        "<?php\nnamespace App\\Controller;\nclass ProductController {\n    #[Route('Rou')]\n    public function detail(): void {}\n}",
    ] {
        let offset = source.rfind("Rou").unwrap() + 3;
        assert!(
            completions(&f.provider, &f.root, source, offset).is_empty(),
            "{source}"
        );
    }

    let missing_cache = tempfile::tempdir().unwrap();
    let missing = PhpIndex::open(missing_cache.path(), None).unwrap();
    let source = "<?php\nnamespace App\\Controller;\nclass ProductController {\n    #[Rou]\n    public function detail(): void {}\n}";
    assert!(
        completions(
            &PhpAttributeCompletionProvider::new(Some(missing)),
            &f.root,
            source,
            source.find("Rou").unwrap() + 3,
        )
        .is_empty()
    );
}

#[test]
fn php_attribute_completion_respects_php_language_level() {
    let f = fixture();
    std::fs::write(
        f.root.path().join("composer.json"),
        r#"{"require":{"php":"7.4"}}"#,
    )
    .unwrap();
    f.php.configure_project(f.root.path()).unwrap();
    let source = r#"<?php
namespace App\Controller;
class ProductController extends \Symfony\Bundle\FrameworkBundle\Controller\AbstractController {
    #[Rou]
    public function detail(): void {}
}"#;
    assert!(
        completions(
            &f.provider,
            &f.root,
            source,
            source.find("Rou").unwrap() + 3
        )
        .is_empty()
    );
}
