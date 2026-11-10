//! Writes to the local disk. A Shopware installation may run on several
//! servers, so files belong in Flysystem; only the temporary directory is
//! the server's own.

use swlsp_parser_php::query as phpquery;
use swlsp_syntax::{SyntaxNode, SyntaxNodeExt};
use swlsp_textutil::lowercase;

use super::php_local::{normalized_local_function_name, php_integer_literal};
use super::php_semantic::{Run, SHOPWARE_PHP_LOCAL_WRITE_CODE};
use super::util::node_text;
use crate::diagnostics::common::{call_target_name, local_call_argument, unwrap_php_parentheses};

const ZIP_ARCHIVE_CLASS: &str = "ZipArchive";
const FILESYSTEM_CLASS: &str = "Symfony\\Component\\Filesystem\\Filesystem";

/// `ZipArchive::CREATE | ZipArchive::OVERWRITE`.
const ZIP_WRITE_FLAGS: i64 = 1 | 8;

/// The writing methods of Symfony's `Filesystem`.
const FILESYSTEM_WRITERS: [&str; 14] = [
    "dumpfile",
    "mkdir",
    "touch",
    "remove",
    "chmod",
    "chown",
    "chgrp",
    "rename",
    "symlink",
    "hardlink",
    "mirror",
    "copy",
    "tempnam",
    "appendtofile",
];

impl Run<'_> {
    pub(super) fn check_filesystem_function(&mut self, call: &SyntaxNode) {
        let name = normalized_local_function_name(call);
        let argument = |parameter: &str, position: usize| {
            local_call_argument(call, parameter, position)
                .map(|value| unwrap_php_parentheses(&value))
        };
        let local = match name.as_str() {
            "file_put_contents" | "unlink" => argument("filename", 0).map(|path| vec![path]),
            "mkdir" | "rmdir" => argument("directory", 0).map(|path| vec![path]),
            "symlink" => argument("link", 1).map(|path| vec![path]),
            "rename" => argument("from", 0)
                .zip(argument("to", 1))
                .map(|(from, to)| vec![from, to]),
            "fopen" => argument("filename", 0)
                .zip(argument("mode", 1))
                .filter(|(_, mode)| is_write_mode(mode))
                .map(|(path, _)| vec![path]),
            _ => None,
        };
        let Some(paths) = local else {
            return;
        };
        if paths.iter().all(is_temporary_path) {
            return;
        }
        self.report_local_write(call, &format!("{name}()"));
    }

    pub(super) fn check_filesystem_method(&mut self, call: &SyntaxNode) {
        let method = phpquery::call_method_name(call);
        let name = lowercase(&method);
        let zip = name == "open";
        if !zip && !FILESYSTEM_WRITERS.contains(&name.as_str()) {
            return;
        }
        let Some(first) = phpquery::argument_expression(call, 0) else {
            return;
        };
        let mut paths = vec![first];
        if zip {
            if !phpquery::argument_expression(call, 1).is_some_and(|flags| creates_archive(&flags))
            {
                return;
            }
        } else if (name == "copy" || name == "rename")
            && let Some(second) = phpquery::argument_expression(call, 1)
        {
            paths.push(second);
        }
        if paths.iter().all(is_temporary_path) {
            return;
        }
        let Some(receiver) = phpquery::call_receiver(call) else {
            return;
        };
        if zip {
            if self.is_instance(&receiver, ZIP_ARCHIVE_CLASS) {
                self.report_local_write(call, "ZipArchive::open() in create mode");
            }
        } else if self.is_instance(&receiver, FILESYSTEM_CLASS) {
            self.report_local_write(call, &format!("Filesystem::{method}()"));
        }
    }

    fn report_local_write(&mut self, call: &SyntaxNode, what: &str) {
        let range = call_target_name(call).map_or_else(
            || call.range_trimmed_trivia(),
            |name| name.range_trimmed_trivia(),
        );
        self.add(
            SHOPWARE_PHP_LOCAL_WRITE_CODE,
            range,
            format!(
                "{what} writes to the local disk; use Flysystem, or a path below \
                 sys_get_temp_dir() for temporary files"
            ),
        );
    }
}

/// A literal `fopen()` mode that creates or changes the file.
fn is_write_mode(mode: &SyntaxNode) -> bool {
    let mode = phpquery::string_value(mode);
    let mut characters = mode.chars();
    characters
        .next()
        .is_some_and(|first| matches!(first, 'w' | 'a' | 'x' | 'c'))
        && characters.all(|character| matches!(character, '+' | 'b'))
}

/// Whether `flags` contain `ZipArchive::CREATE` or `ZipArchive::OVERWRITE`.
fn creates_archive(flags: &SyntaxNode) -> bool {
    let text = node_text(flags);
    text.contains("CREATE")
        || text.contains("OVERWRITE")
        || php_integer_literal(Some(&unwrap_php_parentheses(flags)))
            .is_some_and(|flags| flags & ZIP_WRITE_FLAGS != 0)
}

/// Whether `path` is a standard stream or names the temporary directory:
/// built from `sys_get_temp_dir()` or `tempnam()`, or kept in something
/// called `temp` or `tmp`.
fn is_temporary_path(path: &SyntaxNode) -> bool {
    let text = lowercase(node_text(path).trim());
    ["temp", "tmp", "php://std", "php://memory", "php://output"]
        .iter()
        .any(|marker| text.contains(marker))
        || ["stdin", "stdout", "stderr"].contains(&text.trim_start_matches('\\'))
}
