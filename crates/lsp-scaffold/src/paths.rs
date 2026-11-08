//! Go `path/filepath` and `os` helpers with Go-compatible results.

use std::io;

pub(crate) use swlsp_uriutil::filepath::{abs, base, clean, ext, join, rel, to_slash};

/// Go `filepath.Dir`.
pub(crate) fn dir(path: &str) -> String {
    let volume = swlsp_uriutil::filepath::volume_name_len(path);
    let bytes = path.as_bytes();
    let mut index = bytes.len();
    while index > volume && !swlsp_uriutil::filepath::is_path_separator(bytes[index - 1]) {
        index -= 1;
    }
    let directory = clean(&path[volume..index]);
    if directory == "." && volume > 0 {
        return path[..volume].to_owned();
    }
    format!("{}{}", &path[..volume], directory)
}

/// Go `filepath.EvalSymlinks` for absolute paths.
pub(crate) fn eval_symlinks(path: &str) -> Option<String> {
    std::fs::canonicalize(path)
        .ok()
        .map(|resolved| resolved.to_string_lossy().into_owned())
}

pub(crate) fn resolved_directory_path(path: &str) -> String {
    eval_symlinks(path).unwrap_or_else(|| clean(path))
}

/// Whether a `filepath.Rel` result leaves its base.
pub(crate) fn escapes(relative: &str) -> bool {
    relative == ".." || relative.starts_with("../")
}

/// `rel` that reports escaping (or an error) as `None`.
pub(crate) fn rel_within(base_path: &str, target: &str) -> Option<String> {
    rel(base_path, target)
        .ok()
        .filter(|relative| !escapes(relative))
}

/// Go `*fs.PathError` text: `<op> <path>: <lowercase errno text>`.
pub(crate) fn path_error(op: &str, path: &str, error: &io::Error) -> String {
    format!("{op} {path}: {}", errno_text(error))
}

pub(crate) fn errno_text(error: &io::Error) -> String {
    let text = error.to_string();
    let text = match text.rfind(" (os error ") {
        Some(index) => text[..index].to_owned(),
        None => text,
    };
    let mut characters = text.chars();
    match characters.next() {
        Some(first) => first.to_lowercase().chain(characters).collect(),
        None => text,
    }
}

pub(crate) enum Stat {
    Exists(std::fs::Metadata),
    Missing,
    Failed(io::Error),
}

/// Go `os.Stat` with `os.IsNotExist` classification.
pub(crate) fn stat(path: &str) -> Stat {
    match std::fs::metadata(path) {
        Ok(metadata) => Stat::Exists(metadata),
        Err(error) if error.kind() == io::ErrorKind::NotFound => Stat::Missing,
        Err(error) => Stat::Failed(error),
    }
}

/// Go `os.ReadFile` returning `None` for a missing file.
pub(crate) fn read_file(path: &str) -> Result<Option<String>, String> {
    match std::fs::read(path) {
        Ok(content) => Ok(Some(String::from_utf8_lossy(&content).into_owned())),
        Err(error) if error.kind() == io::ErrorKind::NotFound => Ok(None),
        Err(error) => Err(path_error("open", path, &error)),
    }
}
