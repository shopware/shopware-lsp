use swlsp_uriutil::filepath;

/// Resolves a statically known Symfony configuration resource relative to
/// the declaring config file. Glob matches are sorted; directories and
/// missing targets are excluded.
pub fn resource_files(current_path: &str, resource_path: &str) -> Vec<String> {
    if current_path.is_empty() || resource_path.is_empty() || resource_path.contains('\0') {
        return Vec::new();
    }
    let target = if filepath::is_abs(resource_path) {
        filepath::clean(resource_path)
    } else {
        filepath::join(&[dir(current_path).as_str(), resource_path])
    };
    let candidates = if target.contains(['*', '?', '[']) {
        glob(&target)
    } else {
        vec![target]
    };
    let mut result: Vec<String> = candidates
        .into_iter()
        .filter(|candidate| std::fs::metadata(candidate).is_ok_and(|info| !info.is_dir()))
        .map(|candidate| filepath::clean(&candidate))
        .collect();
    result.sort();
    result
}

/// Go `filepath.Glob`: `*` and `?` never cross a separator; malformed
/// patterns and I/O errors yield no matches.
fn glob(pattern: &str) -> Vec<String> {
    let options = glob::MatchOptions {
        case_sensitive: true,
        require_literal_separator: true,
        require_literal_leading_dot: false,
    };
    let Ok(paths) = glob::glob_with(pattern, options) else {
        return Vec::new();
    };
    paths
        .filter_map(Result::ok)
        .filter_map(|path| path.to_str().map(str::to_owned))
        .collect()
}

/// Go `filepath.Dir` (without volume handling).
fn dir(path: &str) -> String {
    match path.rfind(['/', std::path::MAIN_SEPARATOR]) {
        Some(index) => filepath::clean(&path[..=index]),
        None => ".".to_owned(),
    }
}
