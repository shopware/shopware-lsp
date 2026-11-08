use super::analyze;
use super::php_semantic::php_index_with;
use crate::shopware::*;
use crate::test_support::document;

#[test]
fn shopware_decoration_analyzer_requires_decoration_abstraction() {
    let (_dir, index) = php_index_with(&[
        (
            "/project/src/AbstractProductRoute.php",
            r"<?php
namespace Shopware\Core\Content\Product\SalesChannel;
abstract class AbstractProductRoute
{
    abstract public function getDecorated(): AbstractProductRoute;
}",
        ),
        (
            "/project/src/ProductRoute.php",
            r"<?php
namespace Shopware\Core\Content\Product\SalesChannel;
class ProductRoute extends AbstractProductRoute
{
    public function getDecorated(): AbstractProductRoute { return $this; }
}",
        ),
    ]);
    let document = document(
        "file:///project/src/Consumer.php",
        r"<?php
namespace App;
use Shopware\Core\Content\Product\SalesChannel\ProductRoute;
class Consumer
{
    public function __construct(private ProductRoute $route) {}
}",
    );
    let problems = analyze(&ShopwareDecorationAnalyzer::new(Some(index)), &document);
    assert_eq!(problems.len(), 1);
    assert_eq!(problems[0].id.as_str(), "shopware.decoration.abstraction");
    assert!(problems[0].message.contains("AbstractProductRoute"));
}
