use crate::project_cache_folder_in;

#[test]
fn project_cache_identity() {
    let base = tempfile::tempdir().unwrap();
    let first = project_cache_folder_in(base.path(), "/a/b-c").unwrap();
    let second = project_cache_folder_in(base.path(), "/a-b/c").unwrap();
    assert_ne!(first, second);
    assert!(first.is_dir());
    assert_eq!(
        first,
        project_cache_folder_in(base.path(), "/a/./b-c/").unwrap()
    );
}
