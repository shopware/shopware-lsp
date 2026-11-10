use super::*;
use crate::completion::twig::TwigEnumCompletionProvider;

#[test]
fn twig_enum_completion_lists_php_enums_with_twig_escaping() {
    let (root, php_dir) = (tmp(), tmp());
    let php = php_index(&php_dir);
    index(
        &php,
        &p(&root.path().join("src/Status.php")),
        r"<?php
namespace App\Domain;
enum OrderStatus { case Open; }
class NotAnEnum {}",
    );
    let source = r"{{ enum('App\\Domain\\OrderSta') }}";
    let document = Arc::new(TextDocument::new(
        swlsp_uriutil::file_uri(&p(&root.path().join("templates/order.html.twig"))),
        source,
        1,
    ));
    let offset = (source.find("OrderSta").unwrap() + "OrderSta".len()) as u32;
    let items = complete(
        &TwigEnumCompletionProvider::new(Some(php)),
        &request_with_node(document, offset, offset),
    );
    assert_eq!(items.len(), 1);
    assert_eq!(items[0].label, "App\\Domain\\OrderStatus");
    assert_eq!(items[0].insert_text, r"App\\Domain\\OrderStatus");
}
