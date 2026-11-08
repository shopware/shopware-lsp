mod common;

use std::path::Path;
use std::sync::Arc;

use common::*;
use swlsp_lsp::TextDocument;
use swlsp_lsp_hover::TwigComponentHoverProvider;
use swlsp_lsp_protocol as protocol;
use swlsp_symfony::ServiceIndex;
use swlsp_twig::TwigIndexer;
use swlsp_twigcomponent::Index;

fn component_hover_at(
    provider: &TwigComponentHoverProvider,
    path: &Path,
    source: &str,
    needle: &str,
) -> protocol::Hover {
    let document = TextDocument::new(uri(path), source, 1);
    let request = request_at(document, source.find(needle).unwrap() + 1);
    hover(provider, &request).expect("hover")
}

#[test]
fn twig_live_component_hover_shows_computed_and_writable_props() {
    let root = tempfile::tempdir().unwrap();
    let cache = root.path().join(".cache");
    let php = php_index(&cache.join("php"), &[]);
    let twig = Arc::new(TwigIndexer::new(cache.join("twig"), None).unwrap());
    let components = Arc::new(Index::new(cache.join("components"), None).unwrap());
    components.set_dependencies(Some(php.clone()), None, Some(twig.clone()));
    let class_path = p(&root.path().join("src/Twig/Components/Search.php"));
    let class = r"<?php
namespace App\Twig\Components;
use Symfony\UX\LiveComponent\Attribute\AsLiveComponent;
use Symfony\UX\LiveComponent\Attribute\LiveAction;
use Symfony\UX\LiveComponent\Attribute\LiveArg;
use Symfony\UX\LiveComponent\Attribute\LiveProp;
#[AsLiveComponent]
final class Search {
    #[LiveProp(writable: true)]
    public string $query = '';

    public function getTotal(): int {}

    #[LiveAction]
    public function submit(
        #[LiveArg('pageNumber')] int $page,
        ?string $query = null,
    ): void {}
}";
    index_file(&php, &class_path, class);
    index_file(components.as_ref(), &class_path, class);
    let template_path = root.path().join("templates/components/Search.html.twig");
    let template = r#"{{ computed.total }} {{ query }} {{ live_action('submit') }}
<button data-live-action-param="submit" data-live-page-number-param="2">Next</button>"#;
    write(&template_path, template);
    index_file(twig.as_ref(), &p(&template_path), template);
    let provider = TwigComponentHoverProvider::new(p(root.path()), Some(components));

    let computed = component_hover_at(&provider, &template_path, template, "total");
    assert!(computed.contents.value.contains("Computed component value"));
    assert!(computed.contents.value.contains("PHP type: `int`"));
    assert!(computed.contents.value.contains("cached"));

    let query = component_hover_at(&provider, &template_path, template, "query");
    assert!(query.contents.value.contains("writable from the browser"));

    let action = component_hover_at(&provider, &template_path, template, "submit");
    assert!(action.contents.value.contains("Symfony UX Live Action"));
    assert!(action.contents.value.contains("int $page"));
    assert!(action.contents.value.contains("null|string $query = …"));

    let argument = component_hover_at(&provider, &template_path, template, "page-number");
    assert!(argument.contents.value.contains("Live Action argument"));
    assert!(argument.contents.value.contains("PHP type: `int`"));
    assert!(argument.contents.value.contains("$page"));
    assert!(argument.contents.value.contains("#[LiveArg]"));

    let component = component_hover_at(
        &provider,
        &root.path().join("templates/page.html.twig"),
        "<twig:Search />",
        "Search",
    );
    assert!(component.contents.value.contains("Live component"));
}

#[test]
fn twig_component_hover_shows_compiled_dynamic_template_method() {
    let root = tempfile::tempdir().unwrap();
    let cache = root.path().join(".cache");
    write(
        &root
            .path()
            .join("var/cache/dev-test/Shopware_Core_KernelDevDebugContainer.xml"),
        r#"<container><services>
<service id="ux.twig_component.component_factory">
  <argument/><argument/><argument/><argument/>
  <argument type="collection">
    <argument key="Dynamic" type="collection">
      <argument key="class">App\Twig\Components\Dynamic</argument>
      <argument key="template_from_method">getTemplate</argument>
    </argument>
  </argument>
  <argument type="collection"/>
</service>
</services></container>"#,
    );
    let services =
        Arc::new(ServiceIndex::new(&p(root.path()), cache.join("symfony"), None).unwrap());
    services.reload_compiled_container().unwrap();
    let components = Arc::new(Index::new(cache.join("components"), None).unwrap());
    components.set_dependencies(None, Some(services), None);
    let result = component_hover_at(
        &TwigComponentHoverProvider::new(p(root.path()), Some(components)),
        &root.path().join("templates/page.html.twig"),
        "<twig:Dynamic />",
        "Dynamic",
    );
    assert!(
        result
            .contents
            .value
            .contains("Dynamic template method: `getTemplate`")
    );
    assert!(result.contents.value.contains("compiled Symfony container"));
}
