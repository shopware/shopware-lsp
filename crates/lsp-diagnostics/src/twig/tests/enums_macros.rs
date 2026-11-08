use super::*;
use crate::twig::{TwigEnumAnalyzer, TwigMacroAnalyzer};

#[test]
fn twig_enum_diagnostics_validate_existence_and_enum_kind() {
    let root = tempdir();
    let php_dir = tempdir();
    let php = php_index(php_dir.path());
    index_file(
        &php,
        &root.path().join("src/Types.php").to_string_lossy(),
        "<?php\nnamespace App;\nenum OrderStatus { case Open; }\nclass NotAnEnum {}",
    );
    let source = "{{ enum('App\\\\OrderStatus') }}\n{{ enum_cases('App\\\\OrderStatu') }}\n{{ enum('App\\\\NotAnEnum') }}";
    let document = doc(
        &swlsp_uriutil::file_uri(
            &root
                .path()
                .join("templates/order.html.twig")
                .to_string_lossy(),
        ),
        source,
    );
    let diagnostics = analyze(&TwigEnumAnalyzer::new(Some(php)), &document);
    assert_eq!(diagnostics.len(), 2, "{diagnostics:?}");
    assert_eq!(diagnostics[0].id.as_str(), "twig.enum.missing");
    assert!(diagnostics[0].message.contains("OrderStatu"));
    assert!(suggestions(&diagnostics[0]).contains(&"App\\\\OrderStatus".to_owned()));
    assert_eq!(diagnostics[1].id.as_str(), "twig.enum.invalid");
    assert!(diagnostics[1].message.contains("not an enum"));
}

#[test]
fn twig_macro_diagnostics_report_unknown_imported_macro() {
    let dir = tempdir();
    let index = twig_index(dir.path());
    index_file(
        index.as_ref(),
        "/project/templates/macros/forms.html.twig",
        "{% macro input(name) %}{% endmacro %}",
    );
    let document = doc(
        "file:///project/templates/page.html.twig",
        "{% import 'macros/forms.html.twig' as forms %}\n{{ forms.inpt('email') }}\n{{ forms.input('name') }}\n",
    );
    let diagnostics = analyze(&TwigMacroAnalyzer::new(Some(index)), &document);
    assert_eq!(diagnostics.len(), 1, "{diagnostics:?}");
    assert_eq!(diagnostics[0].id.as_str(), "twig.macro.missing");
    assert!(diagnostics[0].message.contains("inpt"));
    assert!(suggestions(&diagnostics[0]).contains(&"input".to_owned()));
}

#[test]
fn twig_macro_diagnostics_overlay_unsaved_self_macros() {
    let dir = tempdir();
    let index = twig_index(dir.path());
    let document = doc(
        "file:///project/templates/page.html.twig",
        "{% import _self as local %}\n{{ local.helper() }}\n{% macro helper() %}{% endmacro %}\n",
    );
    assert!(analyze(&TwigMacroAnalyzer::new(Some(index)), &document).is_empty());
}
