use super::*;

#[test]
fn ant_path_match_supports_zero_or_nested_double_star_segments() {
    assert!(ant_path_match(
        "src/**/ProductController.php",
        "src/ProductController.php"
    ));
    assert!(ant_path_match(
        "src/**/ProductController.php",
        "src/Admin/Catalog/ProductController.php"
    ));
    assert!(!ant_path_match(
        "src/**/ProductController.php",
        "tests/ProductController.php"
    ));
}
