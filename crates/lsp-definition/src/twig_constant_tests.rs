use swlsp_lsp::{Context, GotoDefinitionProvider, TextDocument};
use swlsp_lsp_protocol as protocol;
use swlsp_uriutil::file_uri;

use super::*;
use crate::twig_common::test_support::*;

#[test]
fn twig_constant_definition_navigates_all_static_forms() {
    let root = tempfile::tempdir().unwrap();
    let cache = tempfile::tempdir().unwrap();
    let root_path = p(root.path());
    let constants_path = format!("{root_path}/src/Constants.php");
    let constants = "<?php
namespace App {
    class CardSuite { public const CLUBS = 'clubs'; }
}
namespace BugDemo {
    const NAMESPACED_CONST = 'value';
}";
    write(&constants_path, constants);
    let php = php_index(cache.path());
    index_php(&php, &constants_path, constants);
    let twig = twig_index(cache.path());
    twig.set_dependencies(Some(php.clone()), None);
    let provider = TwigConstantDefinitionProvider::new(Some(php), Some(twig));

    for (source, needle) in [
        ("{{ constant('App\\\\CardSuite::CLUBS') }}", "CLUBS"),
        (
            "{{ constant('\\\\BugDemo\\\\NAMESPACED_CONST') }}",
            "NAMESPACED_CONST",
        ),
        (
            "{# @var suite \\App\\CardSuite #}\n{{ constant('CLUBS', suite) }}",
            "'CLUBS",
        ),
    ] {
        let offset = (source.rfind(needle).unwrap() + needle.len() / 2) as u32;
        let locations = provider.get_definition(
            &Context::background(),
            &request_at(
                TextDocument::new(
                    file_uri(&format!("{root_path}/templates/constant.html.twig")),
                    source,
                    1,
                ),
                offset,
            ),
        );
        assert_eq!(locations.len(), 1, "{source}");
        assert_eq!(locations[0].uri, file_uri(&constants_path));
        assert_ne!(locations[0].range, protocol::Range::default());
    }
}
