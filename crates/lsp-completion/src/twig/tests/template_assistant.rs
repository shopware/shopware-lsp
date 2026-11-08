use super::*;
use crate::twig::TwigCompletionProvider;

#[test]
fn php_doc_template_assistant_tag_completion() {
    let root = tmp();
    let twig = Arc::new(TwigIndexer::new(root.path().join("twig"), None).unwrap());
    index(
        twig.as_ref(),
        &p(&root.path().join("templates/page.html.twig")),
        "page",
    );
    let php = PhpIndex::open(root.path().join("php"), None).unwrap();
    index(
        &php,
        &p(&root.path().join("src/TemplateAssistant.php")),
        "<?php\n/** @param string $template #Template */\nfunction resolve_template(string $template): void {}\n",
    );
    let source = "<?php resolve_template('page.html');";
    let path = p(&root.path().join("src/Usage.php"));
    let offset = (source.find("page.html").unwrap() + "page.html".len()) as u32;
    let document = Arc::new(TextDocument::new(swlsp_uriutil::file_uri(&path), source, 1));
    let request = request_with_node(document.clone(), offset, offset);
    let provider = TwigCompletionProvider::new(p(root.path()), Some(twig), None, Some(php));
    let item = require_completion(&complete(&provider, &request), "page.html.twig");
    assert_eq!(edit_new_text(&item), "page.html.twig");
    assert_eq!(edit_range_text(&document, &item), "page.html");
}
