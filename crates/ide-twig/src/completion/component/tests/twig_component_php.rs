use std::sync::Arc;

use swlsp_lsp::{CompletionProvider, Context, TextDocument};
use swlsp_lsp_protocol as protocol;

use super::{additional_edit, edit, labels, node_at};
use crate::completion::TwigComponentPhpCompletionProvider;

fn completions(source: &str, offset: usize) -> Vec<protocol::CompletionItem> {
    let document = Arc::new(TextDocument::new(
        "file:///project/src/Component.php",
        source,
        1,
    ));
    let node_offset = offset.min(source.len() - 1);
    let node = node_at(&document, node_offset as u32);
    let request =
        crate::completion::test_support::completion_request(document, node, offset as u32);
    TwigComponentPhpCompletionProvider::new().get_completions(&Context::background(), &request)
}

#[test]
fn twig_component_php_attribute_completion_uses_member_scope() {
    let source = r#"<?php
namespace App\Twig\Components;

use Symfony\UX\TwigComponent\Attribute\AsTwigComponent;

#[AsTwigComponent]
class Card
{
    #[Exp]
    public string $title;

    #[Pre]
    public function mount(): void {}
}
"#;
    let property = completions(source, source.find("Exp").unwrap() + 2);
    assert_eq!(labels(&property), ["ExposeInTemplate"]);
    let method = completions(source, source.find("Pre").unwrap() + 2);
    let mut method_labels = labels(&method);
    method_labels.sort();
    assert_eq!(method_labels, ["ExposeInTemplate", "PostMount", "PreMount"]);

    let pre_mount = method.iter().find(|item| item.label == "PreMount").unwrap();
    assert_eq!(edit(pre_mount).new_text, "PreMount");
    assert_eq!(pre_mount.additional_text_edits.len(), 1);
    assert!(
        additional_edit(pre_mount, 0)
            .new_text
            .contains("use Symfony\\UX\\TwigComponent\\Attribute\\PreMount;")
    );
}

#[test]
fn twig_live_component_php_attribute_completion() {
    let source = r#"<?php
namespace App\Twig\Components;

use Symfony\UX\LiveComponent\Attribute\AsLiveComponent;

#[AsLiveComponent]
class Search
{
    #[Li]
    public string $query = '';

    #[Li]
    public function save(#[Li] string $id): void {}
}
"#;
    let property_offset = source.find("#[Li]").unwrap() + "#[Li".len() - 1;
    let property = labels(&completions(source, property_offset));
    assert!(property.contains(&"LiveProp".to_string()));
    assert!(!property.contains(&"LiveAction".to_string()));

    let method_marker = source[property_offset + 1..].find("#[Li]").unwrap() + property_offset + 1;
    let method = labels(&completions(source, method_marker + "#[Li".len() - 1));
    for expected in [
        "LiveAction",
        "LiveListener",
        "PostHydrate",
        "PreDehydrate",
        "PreReRender",
    ] {
        assert!(method.contains(&expected.to_string()), "{expected}");
    }

    let parameter_marker = source.rfind("#[Li]").unwrap();
    let parameter = labels(&completions(source, parameter_marker + "#[Li".len() - 1));
    assert_eq!(parameter, ["LiveArg"]);
}

#[test]
fn twig_component_php_attribute_completion_handles_incomplete_group() {
    let source = r#"<?php
namespace App\Twig\Components;
use Symfony\UX\TwigComponent\Attribute\AsTwigComponent;
#[AsTwigComponent]
class Card {
    #[Po
    public function mount(): void {}
}"#;
    let items = completions(source, source.find("#[Po").unwrap() + "#[Po".len());
    assert!(labels(&items).contains(&"PostMount".to_string()));
}

#[test]
fn twig_component_php_attribute_completion_rejects_other_classes() {
    let source = r#"<?php
class Ordinary {
    #[Exp]
    public string $title;
}"#;
    assert!(completions(source, source.find("Exp").unwrap() + 2).is_empty());
}
