use swlsp_parser_twig::query as twigquery;
use swlsp_parser_twig::syntax as twigsyntax;

use super::*;

#[test]
fn twig_controller_references_normalize_static_first_arguments() {
    let source = r#"{{ controller('test::action') }}
{{ controller      ('te\\st::action2') }}
{{ render(controller('\\App\\Controller\\NavController::menuAction', {
    menu: current
})) }}
{{ CONTROLLER("app.navigation:menu") }}
{{ controller('DemoBundle:Admin/Nav:show') }}
{{ controller(dynamic) }}
{{ controller("App\\Controller\\#{name}Controller::show") }}"#;
    let parse = swlsp_parser_twig::parse(source);
    let references = twig_controller_references(&parse.root());
    let values: Vec<&str> = references
        .iter()
        .map(|reference| reference.reference.value.as_str())
        .collect();
    assert_eq!(
        values,
        [
            "test::action",
            r"te\st::action2",
            r"App\Controller\NavController::menuAction",
            "app.navigation:menu",
            "DemoBundle:Admin/Nav:show",
        ]
    );
    assert_eq!(
        controller_reference_key(&references[2].reference),
        "app\\controller\\navcontroller::menuaction"
    );
    assert_eq!(references[4].reference.target, "DemoBundle:Admin/Nav");
    assert_eq!(references[4].reference.method, "show");

    let empty = swlsp_parser_twig::parse("{{ controller('') }}");
    let literal = twigquery::nodes(&empty.root(), &[twigsyntax::TWIG_LITERAL_STRING])
        .into_iter()
        .next()
        .unwrap();
    let value = twig_controller_value_at(&literal).unwrap();
    assert!(value.value.is_empty());
}
