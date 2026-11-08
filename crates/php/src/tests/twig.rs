use swlsp_parser_php::query;
use swlsp_parser_php::syntax::PHP_ASSIGNMENT_EXPRESSION;

use super::{index, open};
use crate::TwigTemplateVariable;

fn assert_type(variables: &[TwigTemplateVariable], name: &str, expected: &str) {
    let variable = variables
        .iter()
        .find(|variable| variable.name == name)
        .unwrap_or_else(|| panic!("Twig variable {name:?} not found in {variables:#?}"));
    assert_eq!(variable.ty, expected, "{name}");
}

#[test]
fn collects_typed_twig_template_contexts() {
    let config = tempfile::tempdir().unwrap();
    let idx = open(config.path());
    index(
        &idx,
        "/project/src/ProductController.php",
        r#"<?php
namespace App;

class Product {}

class ProductController
{
    public function show(Product $product)
    {
        $base = ['item' => $product];

        return $this->render(
            parameters: ['product' => $product, ...$base],
            view: 'product/show.html.twig',
        );
    }

    public function alternate(Product $product)
    {
        $context = ['secondary' => $product];

        return $this->render('product/show.html.twig', $context);
    }

    public function storefront(Product $product)
    {
        return $this->renderStorefront(
            '@Storefront/storefront/page/product.html.twig',
            ['page' => $product],
        );
    }

    #[Template('article/detail.html.twig')]
    public function article(Product $product): array
    {
        return ['article' => $product];
    }

    public function helper(Product $product)
    {
        return $this->render(
            'product/helper.html.twig',
            array_merge(['title' => 'Products'], $this->helperData($product)),
        );
    }

    private function helperData(Product $product): array
    {
        return ['related' => $product];
    }
}"#,
    );
    let show = idx
        .twig_template_variables(&["product/show.html.twig"])
        .unwrap();
    assert_type(&show, "product", "App\\Product");
    assert_type(&show, "item", "App\\Product");
    assert_type(&show, "secondary", "App\\Product");

    let storefront = idx
        .twig_template_variables(&["@Storefront/storefront/page/product.html.twig"])
        .unwrap();
    assert_type(&storefront, "page", "App\\Product");

    let article = idx
        .twig_template_variables(&["article/detail.html.twig"])
        .unwrap();
    assert_type(&article, "article", "App\\Product");

    let helper = idx
        .twig_template_variables(&["/product/helper.html.twig"])
        .unwrap();
    assert_type(&helper, "title", "\"Products\"");
    assert_type(&helper, "related", "App\\Product");

    let sources = idx
        .twig_template_variable_sources("product", &["product/show.html.twig"])
        .unwrap();
    assert_eq!(sources.len(), 1);
    assert_eq!(sources[0].file, "/project/src/ProductController.php");
    idx.close().unwrap();
}

#[test]
fn resolves_assigned_twig_context_variable_types() {
    let config = tempfile::tempdir().unwrap();
    let root = tempfile::tempdir().unwrap();
    let idx = open(config.path());
    let controller = r#"<?php
namespace App;

class ProductController
{
    public function __construct(private ProductLoader $loader) {}

    public function show()
    {
        $page = $this->loader->load();

        return $this->render('product/show.html.twig', [
            'page' => $page,
        ]);
    }
}"#;
    let controller_path = root.path().join("src/ProductController.php");
    std::fs::create_dir_all(controller_path.parent().unwrap()).unwrap();
    std::fs::write(&controller_path, controller).unwrap();
    let controller_path = controller_path.to_str().unwrap();
    index(&idx, controller_path, controller);
    let loader = r#"<?php
namespace App;

class Product {}

class ProductLoader
{
    public function load(): Product
    {
        return new Product();
    }
}"#;
    let loader_path = root.path().join("src/ProductLoader.php");
    std::fs::write(&loader_path, loader).unwrap();
    index(&idx, loader_path.to_str().unwrap(), loader);

    let methods = idx.find_methods("App\\ProductLoader", "load");
    assert_eq!(methods.len(), 1);
    assert_eq!(methods[0].return_type.to_string(), "App\\Product");
    let parsed = swlsp_parser_php::parse(controller);
    let document = idx.analyze_tree(controller_path, 0, &parsed.tree);
    let assignments = query::nodes(&parsed.root(), &[PHP_ASSIGNMENT_EXPRESSION]);
    let nodes = crate::twig_context::direct_nodes_for_test(&assignments[0]);
    assert!(nodes.len() >= 2);
    assert_eq!(
        document.type_of(nodes.last().unwrap()).ty.to_string(),
        "App\\Product"
    );

    let variables = idx
        .twig_template_variables(&["product/show.html.twig"])
        .unwrap();
    assert_type(&variables, "page", "App\\Product");
    idx.close().unwrap();
}

#[test]
fn ignores_template_annotations_and_collects_attribute_context() {
    let cache = tempfile::tempdir().unwrap();
    let idx = open(cache.path());
    index(
        &idx,
        "/project/src/Controller/Admin/ProductController.php",
        r#"<?php
namespace App\Controller\Admin;

class Product {}

class ProductController
{
    /** @template T */
    public function map(Product $product): array
    {
        return ['generic' => $product];
    }

    /**
     * @Template("product/legacy.html.twig")
     */
    public function legacyAction(Product $product): array
    {
        return ['legacy' => $product];
    }

    #[Template]
    public function showAction(Product $product): array
    {
        return ['product' => $product];
    }
}"#,
    );
    assert!(
        idx.twig_template_variables(&["product/legacy.html.twig"])
            .unwrap()
            .is_empty()
    );
    assert!(
        idx.twig_template_variables(&["admin/product/map.html.twig"])
            .unwrap()
            .is_empty()
    );
    idx.close().unwrap();

    let idx = open(cache.path());
    for name in ["product/legacy.html.twig", "admin/product/map.html.twig"] {
        assert!(idx.twig_template_variables(&[name]).unwrap().is_empty());
    }
    let guessed = idx
        .twig_template_variables(&["admin/product/show.html.twig"])
        .unwrap();
    assert_type(&guessed, "product", "App\\Controller\\Admin\\Product");
    idx.close().unwrap();
}

#[test]
fn guesses_symfony_template_names() {
    let source = r#"<?php
namespace App\Controller\Shop;
class ProductListingController {
    public function __invoke() {}
    public function showDetailAction() {}
}"#;
    let root = swlsp_parser_php::root(source);
    let methods = query::methods(&query::classes(&root)[0]);
    assert_eq!(
        crate::guessed_controller_template(&root, &methods[0]),
        "shop/product_listing.html.twig"
    );
    assert_eq!(
        crate::guessed_controller_template(&root, &methods[1]),
        "shop/product_listing/show_detail.html.twig"
    );
}
