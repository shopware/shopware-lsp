use super::*;
use crate::completion::twig::TwigComponentVariableCompletionProvider;

#[test]
fn twig_component_variable_completion_uses_class_and_unsaved_props() {
    let root = tmp();
    let cache = root.path().join(".cache");
    let php = PhpIndex::open(&cache, None).unwrap();
    php.configure_project(root.path()).unwrap();
    let twig = Arc::new(TwigIndexer::new(&cache, None).unwrap());
    let components = Arc::new(swlsp_twigcomponent::Index::new(&cache, None).unwrap());
    components.set_dependencies(Some(php.clone()), None, Some(twig.clone()));

    let class_path = p(&root.path().join("src/Twig/Components/Alert.php"));
    let class = r"<?php
namespace App\Twig\Components;
use Symfony\UX\TwigComponent\Attribute\AsTwigComponent;
#[AsTwigComponent]
final class Alert {
    public string $title = '';
    public function getTotal(): int {}
    public function withArgument(string $value): string {}
}";
    index(&php, &class_path, class);
    index(components.as_ref(), &class_path, class);
    let template_path = p(&root.path().join("templates/components/Alert.html.twig"));
    let persisted = "{% props old %}{{ old }}";
    index(twig.as_ref(), &template_path, persisted);
    index(components.as_ref(), &template_path, persisted);

    let provider = TwigComponentVariableCompletionProvider::new(Some(components), Some(php));
    let source = "{% props variant = 'info' %}{{ va }}";
    let document = Arc::new(TextDocument::new(
        swlsp_uriutil::file_uri(&template_path),
        source,
        2,
    ));
    let offset = (source.rfind("va").unwrap() + 1) as u32;
    let items = complete(&provider, &request_with_node(document, offset, offset));
    for label in ["variant", "title", "this", "computed"] {
        require_completion(&items, label);
    }
    assert!(!labels(&items).contains(&"old".to_string()));

    let source = "{{ computed.to }}";
    let document = Arc::new(TextDocument::new(
        swlsp_uriutil::file_uri(&template_path),
        source,
        3,
    ));
    let offset = (source.find("to").unwrap() + 1) as u32;
    let items = complete(&provider, &request_with_node(document, offset, offset));
    require_completion(&items, "total");
    assert!(!labels(&items).contains(&"withArgument".to_string()));
}
