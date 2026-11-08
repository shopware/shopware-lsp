use swlsp_uriutil::filepath;

/// Go `filepath.Dir`.
pub fn dir(path: &str) -> String {
    let volume = filepath::volume_name_len(path);
    let bytes = path.as_bytes();
    let mut index = bytes.len();
    while index > volume && !filepath::is_path_separator(bytes[index - 1]) {
        index -= 1;
    }
    let directory = filepath::clean(&path[volume..index]);
    if directory == "." && volume > 2 {
        return path[..volume].to_owned();
    }
    format!("{}{directory}", &path[..volume])
}

pub(crate) fn join(parts: &[&str]) -> String {
    filepath::join(parts)
}

#[cfg(test)]
mod tests {
    use super::dir;

    #[test]
    fn dir_matches_go() {
        assert_eq!(dir("/a/b/c.php"), "/a/b");
        assert_eq!(dir("c.php"), ".");
        assert_eq!(dir("/c.php"), "/");
        assert_eq!(dir("/a//b/"), "/a/b");
        assert_eq!(dir(""), ".");
    }
}
