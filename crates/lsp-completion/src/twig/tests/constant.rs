use super::*;
use crate::twig::TwigConstantCompletionProvider;

fn constant_items(
    provider: &TwigConstantCompletionProvider,
    root: &str,
    source: &str,
    needle: &str,
) -> Vec<protocol::CompletionItem> {
    let document = Arc::new(TextDocument::new(
        swlsp_uriutil::file_uri(&format!("{root}/templates/constant.html.twig")),
        source,
        1,
    ));
    let offset = (source.rfind(needle).unwrap() + needle.len()) as u32;
    complete(
        provider,
        &request_with_node(document, offset, offset.saturating_sub(1)),
    )
}

#[test]
fn twig_constant_completion_supports_php_constant_forms() {
    let (root, cache) = (tmp(), tmp());
    let root_path = p(root.path());
    let php = php_index(&cache);
    index(
        &php,
        &format!("{root_path}/src/Constants.php"),
        r"<?php
const CONST_FOO = 'foo';
namespace App\Bike;
class FooConst {
    public const CAR = 'car';
    protected const HIDDEN = 'hidden';
}
enum FooEnum { case FOOBAR; }
namespace BugDemo;
const NAMESPACED_CONST = 'value';
class CardSuite {
    public const CLUBS = 'clubs';
    public const SPADES = 'spades';
}",
    );
    let twig = twig_index(&cache);
    twig.set_dependencies(Some(php.clone()), None);
    let provider = TwigConstantCompletionProvider::new(Some(php), Some(twig));

    let general = constant_items(
        &provider,
        &root_path,
        r"{{ constant('App\\Bike\\Foo') }}",
        "Foo",
    );
    let mut names = labels(&general);
    names.sort();
    assert_eq!(names, ["FooConst::CAR", "FooEnum::FOOBAR"]);
    assert_eq!(
        edit_new_text(&require_completion(&general, "FooEnum::FOOBAR")),
        r"App\\Bike\\FooEnum::FOOBAR"
    );

    let explicit = constant_items(
        &provider,
        &root_path,
        r"{{ constant('App\\Bike\\FooConst::C') }}",
        "::C",
    );
    assert_eq!(labels(&explicit), ["CAR"]);
    assert_eq!(edit_new_text(&explicit[0]), r"App\\Bike\\FooConst::CAR");

    let object = constant_items(
        &provider,
        &root_path,
        "{# @var suite \\BugDemo\\CardSuite #}\n{{ constant('CL', suite) }}",
        "'CL",
    );
    assert_eq!(labels(&object), ["CLUBS"]);
    assert_eq!(edit_new_text(&object[0]), "CLUBS");

    let global = constant_items(
        &provider,
        &root_path,
        r"{{ constant('BugDemo\\N') }}",
        "\\N",
    );
    assert_eq!(labels(&global), ["NAMESPACED_CONST"]);

    assert!(
        constant_items(
            &provider,
            &root_path,
            "{{ constant('CL', factory()) }}",
            "'CL"
        )
        .is_empty()
    );
    assert!(!labels(&general).contains(&"HIDDEN".to_string()));
}
