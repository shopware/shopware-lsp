use super::*;
use crate::twig::TwigRenderBlockAnalyzer;

#[test]
fn twig_render_block_diagnostics_report_typos_only_on_abstract_controller() {
    let root = tempdir();
    let php_dir = tempdir();
    let twig_dir = tempdir();
    let php = php_index(php_dir.path());
    let join = |path: &str| root.path().join(path).to_string_lossy().into_owned();
    index_file(
        &php,
        &join("vendor/AbstractController.php"),
        "<?php\nnamespace Symfony\\Bundle\\FrameworkBundle\\Controller;\nabstract class AbstractController {}",
    );
    let twig = twig_index(twig_dir.path());
    index_file(
        twig.as_ref(),
        &join("templates/base.html.twig"),
        "{% block content %}{% endblock %}\n{% block sidebar %}{% endblock %}",
    );
    let source = r#"<?php
namespace App;
class Controller extends \Symfony\Bundle\FrameworkBundle\Controller\AbstractController {
    public function page(): void {
        $this->renderBlock('base.html.twig', 'content');
        $this->renderBlock('base.html.twig', 'contnet');
    }
}
class Unrelated {
    public function page(): void {
        $this->renderBlock('base.html.twig', 'also_missing');
    }
}"#;
    let path = join("src/Controller.php");
    index_file(&php, &path, source);
    let result = analyze(
        &TwigRenderBlockAnalyzer::new(Some(twig), Some(php)),
        &doc(&swlsp_uriutil::file_uri(&path), source),
    );
    assert_eq!(result.len(), 1, "{result:?}");
    assert_eq!(result[0].id.as_str(), "twig.template.block.missing");
    assert!(result[0].message.contains("contnet"));
    assert!(suggestions(&result[0]).contains(&"content".to_owned()));
}
