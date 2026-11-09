//! Recognition of Shopware app `manifest.xml` files.
//!
//! A strict, minimal XML prefix scanner: everything before the first start
//! element (XML declaration, processing instructions, comments, doctype,
//! character data) is validated and skipped, then the root element name and
//! attributes are read. Nothing after the root start tag is inspected, so a
//! manifest that is still being edited further down is recognized.

struct Scanner<'a> {
    source: &'a [u8],
    offset: usize,
    line: usize,
}

/// Why the prefix of a `manifest.xml` is not well-formed XML.
#[derive(Debug, Clone, PartialEq, Eq, thiserror::Error)]
pub enum ManifestError {
    #[error("XML syntax error on line {line}: {message}")]
    Syntax { line: usize, message: String },
    #[error("unsupported XML version {0:?}; only version 1.0 is supported")]
    UnsupportedVersion(String),
    #[error("unsupported XML encoding {0:?}; only UTF-8 is supported")]
    UnsupportedEncoding(String),
}

type ScanResult<T> = Result<T, ManifestError>;

impl<'a> Scanner<'a> {
    fn error(&self, message: impl std::fmt::Display) -> ManifestError {
        ManifestError::Syntax {
            line: self.line,
            message: message.to_string(),
        }
    }

    fn eof(&self) -> ManifestError {
        self.error("unexpected end of input")
    }

    fn peek(&self) -> Option<u8> {
        self.source.get(self.offset).copied()
    }

    fn next(&mut self) -> Option<u8> {
        let byte = self.peek()?;
        self.offset += 1;
        if byte == b'\n' {
            self.line += 1;
        }
        Some(byte)
    }

    fn expect_next(&mut self) -> ScanResult<u8> {
        self.next().ok_or_else(|| self.eof())
    }

    fn starts_with(&self, prefix: &[u8]) -> bool {
        self.source[self.offset..].starts_with(prefix)
    }

    fn skip_space(&mut self) {
        while matches!(self.peek(), Some(b' ' | b'\r' | b'\n' | b'\t')) {
            self.next();
        }
    }

    /// Advances past `terminator`, returning the bytes before it.
    fn until(&mut self, terminator: &[u8]) -> ScanResult<&'a [u8]> {
        let start = self.offset;
        while !self.starts_with(terminator) {
            if self.next().is_none() {
                return Err(self.eof());
            }
        }
        let content = &self.source[start..self.offset];
        for _ in 0..terminator.len() {
            self.next();
        }
        Ok(content)
    }

    fn name(&mut self) -> Option<String> {
        let start = self.offset;
        while let Some(byte) = self.peek() {
            let is_name = byte.is_ascii_alphanumeric()
                || matches!(byte, b'_' | b':' | b'.' | b'-')
                || byte >= 0x80;
            if !is_name {
                break;
            }
            self.next();
        }
        let name = &self.source[start..self.offset];
        let first = *name.first()?;
        if first.is_ascii_digit() || first == b'.' || first == b'-' {
            return None;
        }
        std::str::from_utf8(name).ok().map(str::to_owned)
    }

    fn check_text(&self, text: &[u8]) -> ScanResult<()> {
        let text = std::str::from_utf8(text).map_err(|_| self.error("invalid UTF-8"))?;
        for character in text.chars() {
            let code = character as u32;
            let valid = code == 0x09
                || code == 0x0A
                || code == 0x0D
                || (0x20..=0xD7FF).contains(&code)
                || (0xE000..=0xFFFD).contains(&code)
                || (0x10000..=0x10FFFF).contains(&code);
            if !valid {
                return Err(self.error(format!("illegal character code U+{code:04X}")));
            }
        }
        let mut rest = text;
        while let Some(index) = rest.find('&') {
            rest = &rest[index..];
            let end = rest.find(';').unwrap_or(rest.len());
            if decode_entity(&rest[1..end]).is_none() || end == rest.len() {
                let shown = &rest[..(end + 1).min(rest.len())];
                return Err(self.error(format!("invalid character entity {shown}")));
            }
            rest = &rest[end + 1..];
        }
        Ok(())
    }

    fn character_data(&mut self) -> ScanResult<()> {
        let start = self.offset;
        while self.peek().is_some_and(|byte| byte != b'<') {
            self.next();
        }
        let text = &self.source[start..self.offset];
        self.check_text(text)
    }

    fn processing_instruction(&mut self) -> ScanResult<()> {
        let Some(target) = self.name() else {
            return Err(self.error("expected target name after <?"));
        };
        let content = self.until(b"?>")?;
        if target != "xml" {
            return Ok(());
        }
        let content = String::from_utf8_lossy(content);
        let version = proc_inst("version", &content);
        if !version.is_empty() && version != "1.0" {
            return Err(ManifestError::UnsupportedVersion(version));
        }
        let encoding = proc_inst("encoding", &content);
        if !encoding.is_empty() && !encoding.eq_ignore_ascii_case("utf-8") {
            return Err(ManifestError::UnsupportedEncoding(encoding));
        }
        Ok(())
    }

    fn comment(&mut self) -> ScanResult<()> {
        let (mut previous2, mut previous1) = (0u8, 0u8);
        loop {
            let byte = self.expect_next()?;
            if previous2 == b'-' && previous1 == b'-' {
                if byte != b'>' {
                    return Err(self.error(r#"invalid sequence "--" not allowed in comments"#));
                }
                return Ok(());
            }
            previous2 = previous1;
            previous1 = byte;
        }
    }

    fn directive(&mut self) -> ScanResult<()> {
        let mut depth = 0usize;
        let mut quote = 0u8;
        loop {
            let byte = self.expect_next()?;
            if quote != 0 {
                if byte == quote {
                    quote = 0;
                }
                continue;
            }
            match byte {
                b'\'' | b'"' => quote = byte,
                b'<' => depth += 1,
                b'>' if depth == 0 => return Ok(()),
                b'>' => depth -= 1,
                _ => {}
            }
        }
    }

    fn attribute_value(&mut self) -> ScanResult<String> {
        let quote = self.expect_next()?;
        if quote != b'"' && quote != b'\'' {
            return Err(self.error("unquoted or missing attribute value in element"));
        }
        let start = self.offset;
        loop {
            match self.expect_next()? {
                b'<' => return Err(self.error("unescaped < inside quoted string")),
                byte if byte == quote => break,
                _ => {}
            }
        }
        let raw = &self.source[start..self.offset - 1];
        self.check_text(raw)?;
        Ok(unescape(&String::from_utf8_lossy(raw)))
    }

    /// Reads a start tag after `<`, returning the local name and attribute
    /// values.
    fn start_element(&mut self) -> ScanResult<(String, Vec<String>)> {
        let Some(name) = self.name() else {
            return Err(self.error("expected element name after <"));
        };
        let local = local_name(&name).ok_or_else(|| self.error("expected element name after <"))?;
        let mut attributes = Vec::new();
        loop {
            self.skip_space();
            match self.expect_next()? {
                b'/' => {
                    if self.expect_next()? != b'>' {
                        return Err(self.error("expected /> in element"));
                    }
                    break;
                }
                b'>' => break,
                _ => {
                    self.offset -= 1;
                    if self.name().as_deref().and_then(local_name).is_none() {
                        return Err(self.error("expected attribute name in element"));
                    }
                    self.skip_space();
                    if self.expect_next()? != b'=' {
                        return Err(self.error("attribute name without = in element"));
                    }
                    self.skip_space();
                    attributes.push(self.attribute_value()?);
                }
            }
        }
        Ok((local, attributes))
    }
}

/// The local part of a possibly prefixed name: at most one colon is allowed;
/// a prefix or local part that would be empty keeps the whole name as local.
fn local_name(name: &str) -> Option<String> {
    if name.matches(':').count() > 1 {
        return None;
    }
    match name.split_once(':') {
        Some((space, local)) if !space.is_empty() && !local.is_empty() => Some(local.to_owned()),
        _ => Some(name.to_owned()),
    }
}

/// The quoted value of `param=` in an XML declaration.
fn proc_inst(param: &str, content: &str) -> String {
    let mut rest = content;
    loop {
        let Some(index) = rest.find(param) else {
            return String::new();
        };
        rest = rest[index + param.len()..].trim_start();
        let Some(after_equals) = rest.strip_prefix('=') else {
            continue;
        };
        let after_equals = after_equals.trim_start();
        let Some(quote) = after_equals
            .chars()
            .next()
            .filter(|c| *c == '"' || *c == '\'')
        else {
            return String::new();
        };
        let value = &after_equals[1..];
        return match value.find(quote) {
            Some(end) => value[..end].to_owned(),
            None => String::new(),
        };
    }
}

fn decode_entity(name: &str) -> Option<char> {
    match name {
        "lt" => Some('<'),
        "gt" => Some('>'),
        "amp" => Some('&'),
        "apos" => Some('\''),
        "quot" => Some('"'),
        _ => {
            let number = name.strip_prefix('#')?;
            let code = match number.strip_prefix('x') {
                Some(hex) => u32::from_str_radix(hex, 16).ok()?,
                None => number.parse().ok()?,
            };
            char::from_u32(code)
        }
    }
}

fn unescape(value: &str) -> String {
    let mut result = String::with_capacity(value.len());
    let mut rest = value;
    while let Some(index) = rest.find('&') {
        result.push_str(&rest[..index]);
        rest = &rest[index..];
        let end = rest.find(';').unwrap_or(0);
        match decode_entity(&rest[1..end.max(1)]) {
            Some(character) if end > 0 => {
                result.push(character);
                rest = &rest[end + 1..];
            }
            _ => {
                result.push('&');
                rest = &rest[1..];
            }
        }
    }
    result.push_str(rest);
    result
}

/// Reports whether `content` is a Shopware app manifest: its root element
/// is `manifest` (case-insensitive) with an attribute value mentioning both
/// "shopware" and "manifest", typically the schema location.
pub(crate) fn is_shopware_manifest(content: &[u8]) -> Result<bool, ManifestError> {
    let mut scanner = Scanner {
        source: content,
        offset: 0,
        line: 1,
    };
    loop {
        if scanner.peek().is_none() {
            return Ok(false);
        }
        if scanner.peek() != Some(b'<') {
            scanner.character_data()?;
            continue;
        }
        scanner.next();
        if scanner.starts_with(b"?") {
            scanner.next();
            scanner.processing_instruction()?;
        } else if scanner.starts_with(b"!--") {
            scanner.offset += 3;
            scanner.comment()?;
        } else if scanner.starts_with(b"![CDATA[") {
            scanner.offset += 8;
            let text = scanner.until(b"]]>")?;
            scanner.check_text(text)?;
        } else if scanner.starts_with(b"!") {
            scanner.next();
            scanner.directive()?;
        } else if scanner.starts_with(b"/") {
            scanner.next();
            let name = scanner.name().unwrap_or_default();
            return Err(scanner.error(format!("unexpected end element </{name}>")));
        } else {
            let (local, attributes) = scanner.start_element()?;
            if !local.eq_ignore_ascii_case("manifest") {
                return Ok(false);
            }
            return Ok(attributes.iter().any(|value| {
                let value = value.to_lowercase();
                value.contains("shopware") && value.contains("manifest")
            }));
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    const SCHEMA: &str = "https://raw.githubusercontent.com/shopware/platform/trunk/src/Core/Framework/App/Manifest/Schema/manifest-3.0.xsd";

    #[test]
    fn recognizes_manifest_schema() {
        let manifest = format!(
            "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<!-- app -->\n<!DOCTYPE manifest>\n<manifest xmlns:xsi=\"http://www.w3.org/2001/XMLSchema-instance\"\n  xsi:noNamespaceSchemaLocation=\"{SCHEMA}\"><meta/></manifest>"
        );
        assert_eq!(is_shopware_manifest(manifest.as_bytes()), Ok(true));
        assert_eq!(
            is_shopware_manifest(format!("<x:Manifest a='{SCHEMA}'/>").as_bytes()),
            Ok(true)
        );
        assert_eq!(is_shopware_manifest(b"<manifest/>"), Ok(false));
        assert_eq!(
            is_shopware_manifest(b"<config a='shopware manifest'>"),
            Ok(false)
        );
        assert_eq!(is_shopware_manifest(b""), Ok(false));
        assert_eq!(is_shopware_manifest(b"  \n"), Ok(false));
        // Only the root start tag is inspected.
        assert_eq!(
            is_shopware_manifest(b"<manifest a='shopware/manifest'><broken"),
            Ok(true)
        );
        assert_eq!(
            is_shopware_manifest(b"<manifest a='shop&amp;ware manifest'>"),
            Ok(false)
        );
    }

    #[test]
    fn rejects_malformed_prefixes() {
        for source in [
            "<manifest a=unquoted>",
            "<manifest a>",
            "<manifest a='<'>",
            "<!-- unterminated",
            "<!-- a -- b -->",
            "</manifest>",
            "<?xml version=\"1.1\"?><manifest/>",
            "<?xml version=\"1.0\" encoding=\"ISO-8859-1\"?><manifest/>",
            "&bogus; <manifest/>",
            "<a:b:c/>",
            "<manifest",
            "<1manifest/>",
        ] {
            assert!(is_shopware_manifest(source.as_bytes()).is_err(), "{source}");
        }
        assert_eq!(
            is_shopware_manifest(b"\n<manifest a=x>")
                .unwrap_err()
                .to_string(),
            "XML syntax error on line 2: unquoted or missing attribute value in element"
        );
        assert_eq!(
            is_shopware_manifest(b"<?xml version='1.0' encoding='ISO-8859-1'?>")
                .unwrap_err()
                .to_string(),
            r#"unsupported XML encoding "ISO-8859-1"; only UTF-8 is supported"#
        );
    }
}
