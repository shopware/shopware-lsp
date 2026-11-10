use std::collections::HashSet;
use std::sync::Arc;

use swlsp_lsp::Context;
use swlsp_twigcomponent::{Index, LiveAction, LiveActionParameter};

use super::*;
use crate::diagnostics::twig::component::Run;
use crate::diagnostics::twig::{LiveComponentEventAnalyzer, TwigComponentAnalyzer};

fn s(path: &Path) -> String {
    path.to_string_lossy().into_owned()
}

fn component_index(dir: &Path) -> Arc<Index> {
    Arc::new(Index::new(dir, None).expect("component index"))
}

fn has_suggestion(problem: &Problem, value: &str) -> bool {
    suggestions(problem)
        .iter()
        .any(|candidate| candidate == value)
}

#[test]
fn twig_component_diagnostics_missing_and_mixed_syntax() {
    let root = tempdir();
    let twig = twig_index(&root.path().join(".cache"));
    let components = component_index(&root.path().join(".cache"));
    components.set_dependencies(None, None, Some(twig.clone()));
    index_file(
        twig.as_ref(),
        &s(&root.path().join("templates/components/Alert.html.twig")),
        "{% block content %}{% endblock %}",
    );
    let (_, values) = check_problems(
        &TwigComponentAnalyzer::new(Some(components)),
        "file:///project/templates/page.html.twig",
        "<twig:{|twig.component.missing:Alret|} />\n<twig:Alert>\n  <twig:block name=\"{|twig.component.block.missing:contnt|}\">Hi</twig:block>\n  {% block {|twig.component.mixed_syntax:content|} %}Hi{% endblock %}\n</twig:Alert>",
    );
    assert!(has_suggestion(&values[0], "Alert"));
    assert!(has_suggestion(&values[1], "content"));
}

#[test]
fn twig_component_diagnostics_reject_self_macro_imports() {
    let root = tempdir();
    let twig = twig_index(&root.path().join(".cache"));
    let components = component_index(&root.path().join(".cache"));
    components.set_dependencies(None, None, Some(twig.clone()));
    index_file(
        twig.as_ref(),
        &s(&root.path().join("templates/components/Alert.html.twig")),
        "Alert",
    );
    let (_, values) = check_problems(
        &TwigComponentAnalyzer::new(Some(components)),
        "file:///project/templates/page.html.twig",
        "{% from _self import outside %}
<twig:Alert>
  {% from {|twig.component.self_macro_import:_self|} import html_component %}
  {% from 'macros.html.twig' import valid %}
</twig:Alert>
{% component 'Alert' %}
  {% from {|twig.component.self_macro_import:_self|} import twig_component %}
{% endcomponent %}",
    );
    for value in &values {
        assert!(value.message.contains("full template path"));
    }
}

const CART_CLASS: &str = "<?php
namespace App\\Twig\\Components;
use Symfony\\UX\\LiveComponent\\Attribute\\AsLiveComponent;
use Symfony\\UX\\LiveComponent\\Attribute\\LiveAction;
use Symfony\\UX\\LiveComponent\\Attribute\\LiveArg;
#[AsLiveComponent]
final class Cart {
    #[LiveAction]
    public function save(#[LiveArg('itemId')] int $id): void {}
}";

#[test]
fn twig_component_diagnostics_report_missing_live_action() {
    let root = tempdir();
    let cache = root.path().join(".cache");
    let php = php_index(&cache);
    let twig = twig_index(&cache);
    let components = component_index(&cache);
    components.set_dependencies(Some(php.clone()), None, Some(twig.clone()));
    let class_path = s(&root.path().join("src/Twig/Components/Cart.php"));
    index_file(&php, &class_path, CART_CLASS);
    index_file(components.as_ref(), &class_path, CART_CLASS);
    let template_path = s(&root.path().join("templates/components/Cart.html.twig"));
    let fixture = "{{ live_action('save') }}
<button data-live-action-param=\"debounce(300)|{|twig.component.live_action.missing:svae|}\">Typo</button>
<button data-live-action-param=\"save\" data-live-{|twig.component.live_argument.missing:itme-id|}-param=\"42\">Argument typo</button>
<button data-live-action-param=\"{{ dynamicAction }}\">Dynamic</button>";
    index_file(twig.as_ref(), &template_path, &plain(fixture));
    let (_, values) = check_problems(
        &TwigComponentAnalyzer::new(Some(components)),
        &format!("file://{template_path}"),
        fixture,
    );
    assert!(has_suggestion(&values[0], "save"));
    assert!(has_suggestion(&values[1], "item-id"));
}

#[test]
fn twig_component_diagnostics_stops_for_canceled_document() {
    let dir = tempdir();
    let index = component_index(dir.path());
    let ctx = Context::background();
    ctx.token().cancel();
    let document = doc(
        "file:///project/template.twig",
        "<twig:Missing>{% from _self import render %}</twig:Missing>",
    );
    let problems = TwigComponentAnalyzer::new(Some(index))
        .analyze(&ctx, &document)
        .unwrap();
    assert!(problems.is_empty());
}

#[test]
fn twig_component_live_arguments_preserve_case_and_unknown_action_behavior() {
    let document = doc(
        "file:///project/template.twig",
        "
<button data-live-action-param=\"SAVE\" data-live-item-id-param=\"1\">Valid</button>
<button data-live-action-param=\"save\" data-live-itme-id-param=\"1\">Typo</button>
<button data-live-action-param=\"unknown\" data-live-itme-id-param=\"1\">Unknown action</button>
<button data-live-action-param=\"empty\" data-live-itme-id-param=\"1\">No parameters</button>",
    );
    let dir = tempdir();
    let index = component_index(dir.path());
    let root = document.root().unwrap();
    let ctx = Context::background();
    let mut run = Run {
        ctx: &ctx,
        index: &index,
        root: &root,
        path: "/project/template.twig".into(),
        names: Vec::new(),
        available: HashSet::new(),
        problems: Vec::new(),
    };
    let actions = vec![
        LiveAction {
            name: "save".into(),
            parameters: vec![LiveActionParameter {
                name: "itemId".into(),
                ..LiveActionParameter::default()
            }],
            ..LiveAction::default()
        },
        LiveAction {
            name: "empty".into(),
            ..LiveAction::default()
        },
    ];
    run.collect_missing_live_actions(&actions);
    run.collect_missing_live_arguments(&actions);
    assert_eq!(run.problems.len(), 2, "{:?}", run.problems);
    assert_eq!(
        run.problems[0].id.as_str(),
        "twig.component.live_action.missing"
    );
    assert_eq!(range_text(&document, run.problems[0].range), "unknown");
    assert_eq!(
        run.problems[1].id.as_str(),
        "twig.component.live_argument.missing"
    );
    assert_eq!(range_text(&document, run.problems[1].range), "itme-id");
    assert!(has_suggestion(&run.problems[1], "item-id"));
}

#[test]
fn twig_component_block_lookups_refresh_between_analyses() {
    let root = tempdir();
    let twig = twig_index(&root.path().join("cache"));
    let components = component_index(&root.path().join("cache"));
    components.set_dependencies(None, None, Some(twig.clone()));
    index_file(
        twig.as_ref(),
        &s(&root.path().join("templates/components/Notice.html.twig")),
        "{% block content %}{% endblock %}",
    );
    let path = s(&root.path().join("templates/components/Alert.html.twig"));
    let missing = "<twig:Alert><twig:block name=\"{|twig.component.block.missing:content|}\"/></twig:Alert><twig:Alert><twig:block name=\"{|twig.component.block.missing:content|}\"/></twig:Alert><twig:Notice><twig:block name=\"content\"/></twig:Notice>";
    let found = plain(missing);
    let analyzer = TwigComponentAnalyzer::new(Some(components));
    for template in [
        "no blocks",
        "{% block content %}{% endblock %}",
        "{% block footer %}{% endblock %}",
    ] {
        index_file(twig.as_ref(), &path, template);
        let fixture = if template.contains("block content") {
            found.as_str()
        } else {
            missing
        };
        check_problems(&analyzer, "file:///project/page.twig", fixture);
    }
}

#[test]
fn live_component_event_diagnostics_report_unknown_events_and_payload() {
    let root = tempdir();
    let cache = root.path().join(".cache");
    let php = php_index(&cache);
    let components = component_index(&cache);
    components.set_dependencies(Some(php.clone()), None, None);
    let class_path = s(&root.path().join("src/Twig/Components/Cart.php"));
    let class = "<?php
namespace App\\Twig\\Components;
use Symfony\\UX\\LiveComponent\\Attribute\\AsLiveComponent;
use Symfony\\UX\\LiveComponent\\Attribute\\LiveArg;
use Symfony\\UX\\LiveComponent\\Attribute\\LiveListener;
#[AsLiveComponent]
final class Cart {
    #[LiveListener('productAdded')]
    public function refresh(
        #[LiveArg('productId')] int $id,
    ): void {}
}";
    index_file(&php, &class_path, class);
    index_file(components.as_ref(), &class_path, class);
    let provider = LiveComponentEventAnalyzer::new(Some(components));

    let php_path = s(&root.path().join("src/Emitter.php"));
    let (_, values) = check_problems(
        &provider,
        &swlsp_uriutil::file_uri(&php_path),
        "<?php class Emitter {
    public function save(string $dynamic): void {
        $this->emit('{|twig.component.live_event.missing:prodcutAdded|}');
        $this->emit('productAdded', ['{|twig.component.live_event_argument.missing:prodcutId|}' => 42]);
        $this->emit($dynamic);
    }
}",
    );
    assert!(has_suggestion(&values[0], "productAdded"));
    assert!(has_suggestion(&values[1], "productId"));

    let twig_path = s(&root.path().join("templates/Cart.html.twig"));
    let (_, values) = check_problems(
        &provider,
        &swlsp_uriutil::file_uri(&twig_path),
        "<button
    data-action=\"click->live#emit\"
    data-live-event-param=\"productAdded\"
    data-live-{|twig.component.live_event_argument.missing:prodcut-id|}-param=\"42\"
></button>",
    );
    assert!(has_suggestion(&values[0], "product-id"));
}
