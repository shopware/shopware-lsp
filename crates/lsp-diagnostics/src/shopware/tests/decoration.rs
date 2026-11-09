use super::php_semantic::php_index_with;
use crate::shopware::*;
use crate::test_support::check_problems;

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
    let (_, problems) = check_problems(
        &ShopwareDecorationAnalyzer::new(Some(index)),
        "file:///project/src/Consumer.php",
        r"<?php
namespace App;
use Shopware\Core\Content\Product\SalesChannel\ProductRoute;
class Consumer
{
    public function __construct({|shopware.decoration.abstraction:private ProductRoute $route|}) {}
}",
    );
    assert!(problems[0].message.contains("AbstractProductRoute"));
}
