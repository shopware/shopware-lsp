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

mod manage {
    use std::path::Path;

    use serde_json::json;
    use swlsp_lsp::Context;

    use crate::cache_manage::commands;
    use crate::tests::workspace_test::open_workspace;
    use crate::{CacheState, ClearScope, clear_caches, list_caches, project_cache_folder_in};

    /// Opens and closes a workspace, leaving its cache behind.
    fn used_cache(project: &Path, base: &Path) -> std::path::PathBuf {
        let workspace = open_workspace(project, base);
        workspace.close_workspace().unwrap();
        workspace.cache_dir().to_owned()
    }

    fn state_of(base: &Path, cache: &Path) -> (CacheState, bool, bool) {
        let entry = list_caches(base)
            .unwrap()
            .into_iter()
            .find(|entry| entry.path == cache)
            .expect("cache is listed");
        (entry.state, entry.in_use, entry.stale)
    }

    #[test]
    fn caches_are_identified_and_classified() {
        let base = tempfile::tempdir().unwrap();
        assert!(list_caches(base.path()).unwrap().is_empty());

        let open_project = tempfile::tempdir().unwrap();
        let open = open_workspace(open_project.path(), base.path());
        let closed_project = tempfile::tempdir().unwrap();
        let closed = used_cache(closed_project.path(), base.path());
        let gone_project = tempfile::tempdir().unwrap();
        let gone = used_cache(gone_project.path(), base.path());
        drop(gone_project);
        // A directory an older server wrote: no root, written just now.
        let unknown = base.path().join("shopware-lsp").join("legacy");
        std::fs::create_dir_all(&unknown).unwrap();
        std::fs::write(unknown.join("indexes.db"), "data").unwrap();
        // The same project with an index of another version.
        let outdated_project = tempfile::tempdir().unwrap();
        let outdated = used_cache(outdated_project.path(), base.path());
        std::fs::write(outdated.join("index_version"), "1").unwrap();

        let caches = list_caches(base.path()).unwrap();
        assert_eq!(caches.len(), 5);
        let open_entry = caches
            .iter()
            .find(|entry| entry.path == open.cache_dir())
            .unwrap();
        assert_eq!(
            open_entry.root.as_deref(),
            Some(open_project.path().to_str().unwrap())
        );
        assert!(open_entry.bytes > 0 && open_entry.files > 0);
        assert_eq!(
            state_of(base.path(), open.cache_dir()),
            (CacheState::Current, true, false)
        );
        assert_eq!(
            state_of(base.path(), &closed),
            (CacheState::Current, false, false)
        );
        assert_eq!(
            state_of(base.path(), &gone),
            (CacheState::ProjectGone, false, true)
        );
        // Recently written caches may belong to a server that does not
        // lock them: only a week without use makes them stale.
        assert_eq!(
            state_of(base.path(), &unknown),
            (CacheState::Unknown, false, false)
        );
        assert_eq!(
            state_of(base.path(), &outdated),
            (CacheState::Outdated, false, false)
        );
        open.close_workspace().unwrap();
    }

    #[test]
    fn clearing_respects_the_scope_and_open_workspaces() {
        let base = tempfile::tempdir().unwrap();
        let open_project = tempfile::tempdir().unwrap();
        let open = open_workspace(open_project.path(), base.path());
        let closed_project = tempfile::tempdir().unwrap();
        let closed = used_cache(closed_project.path(), base.path());
        let gone_project = tempfile::tempdir().unwrap();
        let gone = used_cache(gone_project.path(), base.path());
        drop(gone_project);
        let outside = base.path().join("unrelated.txt");
        std::fs::write(&outside, "keep").unwrap();

        // A dry run reports and deletes nothing.
        let preview = clear_caches(base.path(), ClearScope::Stale, true).unwrap();
        assert!(preview.dry_run);
        assert_eq!(preview.cleared.len(), 1);
        assert!(gone.is_dir());

        let stale = clear_caches(base.path(), ClearScope::Stale, false).unwrap();
        assert_eq!(stale.cleared.len(), 1);
        assert_eq!(stale.cleared[0].path, gone);
        assert!(stale.freed_bytes > 0 && stale.in_use.is_empty());
        assert!(!gone.exists() && closed.is_dir());

        // The cache of an open workspace is kept, whatever the scope.
        let root = open_project.path().to_str().unwrap();
        let own = clear_caches(base.path(), ClearScope::Project(root), false).unwrap();
        assert!(own.cleared.is_empty());
        assert_eq!(own.in_use.len(), 1);
        let all = clear_caches(base.path(), ClearScope::All, false).unwrap();
        assert_eq!(all.cleared.len(), 1);
        assert_eq!(all.cleared[0].path, closed);
        assert_eq!(all.in_use.len(), 1);
        assert!(!closed.exists() && open.cache_dir().is_dir());

        // Once closed and dropped it can go; a project without a cache
        // reports nothing and leaves no empty directory behind.
        let open_cache = open.cache_dir().to_owned();
        open.close_workspace().unwrap();
        drop(open);
        let own = clear_caches(base.path(), ClearScope::Project(root), false).unwrap();
        assert_eq!(own.cleared.len(), 1);
        assert!(!open_cache.exists());
        let again = clear_caches(base.path(), ClearScope::Project(root), false).unwrap();
        assert!(again.cleared.is_empty() && again.in_use.is_empty());
        assert!(!open_cache.exists());
        assert_eq!(std::fs::read_to_string(&outside).unwrap(), "keep");
        // The workspace still finds its place after a clear.
        assert_eq!(
            project_cache_folder_in(base.path(), root).unwrap(),
            open_cache
        );
    }

    #[test]
    fn editors_list_and_clear_through_commands() {
        let base = tempfile::tempdir().unwrap();
        let gone_project = tempfile::tempdir().unwrap();
        let gone = used_cache(gone_project.path(), base.path());
        drop(gone_project);
        let commands: std::collections::HashMap<_, _> = commands(base.path()).into_iter().collect();
        let ctx = Context::background();

        let listed = commands["shopware/cache/list"](&ctx, None).unwrap();
        assert_eq!(listed["caches"][0]["state"], "projectGone");
        assert_eq!(listed["caches"][0]["stale"], true);

        let clear = &commands["shopware/cache/clear"];
        // The server's own cache is rebuilt in place, not cleared.
        assert!(clear(&ctx, Some(&json!({"scope": "project"}))).is_err());
        let preview = clear(&ctx, Some(&json!({"scope": "stale", "dryRun": true}))).unwrap();
        assert_eq!(preview["dryRun"], true);
        assert!(gone.is_dir());
        let cleared = clear(&ctx, None).unwrap();
        assert_eq!(cleared["cleared"].as_array().unwrap().len(), 1);
        assert!(cleared["freedBytes"].as_u64().unwrap() > 0);
        assert!(!gone.exists());
    }
}
