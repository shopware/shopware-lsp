//! Classification of SQL schema statements, as far as migration rules need
//! it: which statement a text holds, which table it is about, and what an
//! `ALTER TABLE` does to it.
//!
//! This is no SQL parser. A statement is tokenized (identifiers, backtick
//! and string quoting, comments, parentheses) and read by its leading
//! keywords; expressions, column types and anything unknown are skipped.
//! Text that is no SQL, or SQL assembled from placeholders, classifies as
//! [`Kind::Other`] or with an empty table name, never as something else.
//! Every position is a byte range into the given text.

use std::ops::Range;

/// One statement of a SQL text.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Statement {
    /// The statement without the trailing semicolon and blanks around it.
    pub range: Range<usize>,
    pub kind: Kind,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub enum Kind {
    CreateTable {
        table: String,
        if_not_exists: bool,
        temporary: bool,
        /// `CREATE [TEMPORARY] TABLE`, where `IF NOT EXISTS` goes after.
        keywords: Range<usize>,
    },
    /// `CREATE [UNIQUE|FULLTEXT] INDEX name ON table`.
    CreateIndex {
        table: String,
        index: String,
        if_not_exists: bool,
    },
    AlterTable {
        table: String,
        actions: Vec<Action>,
    },
    DropTable {
        tables: Vec<String>,
        if_exists: bool,
    },
    /// `RENAME TABLE a TO b`.
    RenameTable {
        tables: Vec<String>,
    },
    /// `DROP INDEX name ON table`.
    DropIndex {
        table: String,
        index: String,
    },
    Insert {
        table: String,
        /// `INSERT IGNORE`, `REPLACE` or `ON DUPLICATE KEY UPDATE`: a row
        /// that exists is no error.
        tolerant: bool,
    },
    Other,
}

/// One comma-separated part of an `ALTER TABLE`.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Action {
    pub range: Range<usize>,
    pub kind: ActionKind,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub enum ActionKind {
    AddColumn {
        column: String,
        if_not_exists: bool,
        /// `NOT NULL` without a `DEFAULT` (and not generated).
        required: bool,
    },
    /// `ADD INDEX|KEY|UNIQUE|FULLTEXT|CONSTRAINT|FOREIGN KEY|PRIMARY KEY`.
    AddKey {
        name: String,
        if_not_exists: bool,
    },
    DropColumn {
        column: String,
        if_exists: bool,
    },
    /// `DROP INDEX|KEY|FOREIGN KEY|CONSTRAINT|PRIMARY KEY`.
    DropKey {
        name: String,
        if_exists: bool,
    },
    /// `RENAME COLUMN a TO b`, or `CHANGE a b …` with another name.
    RenameColumn {
        from: String,
        to: String,
    },
    /// `RENAME [TO|AS] name`.
    RenameTable {
        to: String,
    },
    Other,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum TokenKind {
    /// A bare word, a backtick-quoted name or a double-quoted name.
    Word,
    QuotedName,
    String,
    Symbol,
}

#[derive(Debug, Clone)]
struct Token<'a> {
    kind: TokenKind,
    /// The word, or the name without its quotes.
    text: &'a str,
    range: Range<usize>,
}

impl Token<'_> {
    fn is(&self, keyword: &str) -> bool {
        self.kind == TokenKind::Word && self.text.eq_ignore_ascii_case(keyword)
    }

    fn is_symbol(&self, symbol: &str) -> bool {
        self.kind == TokenKind::Symbol && self.text == symbol
    }

    fn is_name(&self) -> bool {
        matches!(self.kind, TokenKind::Word | TokenKind::QuotedName)
    }
}

fn tokenize(sql: &str) -> Vec<Token<'_>> {
    let bytes = sql.as_bytes();
    let mut tokens = Vec::new();
    let mut index = 0;
    while index < bytes.len() {
        let byte = bytes[index];
        let start = index;
        if byte.is_ascii_whitespace() {
            index += 1;
        } else if sql[index..].starts_with("--") || byte == b'#' {
            index = sql[index..]
                .find('\n')
                .map_or(bytes.len(), |end| index + end);
        } else if sql[index..].starts_with("/*") {
            index = sql[index + 2..]
                .find("*/")
                .map_or(bytes.len(), |end| index + 2 + end + 2);
        } else if byte == b'`' || byte == b'\'' || byte == b'"' {
            index += 1;
            while index < bytes.len() && bytes[index] != byte {
                index += if bytes[index] == b'\\' { 2 } else { 1 };
            }
            let end = index.min(bytes.len());
            index = (end + 1).min(bytes.len());
            let Some(text) = sql.get(start + 1..end) else {
                continue;
            };
            tokens.push(Token {
                kind: if byte == b'\'' {
                    TokenKind::String
                } else {
                    TokenKind::QuotedName
                },
                text,
                range: start..index,
            });
        } else if byte.is_ascii_alphanumeric() || byte == b'_' || byte >= 0x80 {
            while index < bytes.len()
                && (bytes[index].is_ascii_alphanumeric()
                    || bytes[index] == b'_'
                    || bytes[index] >= 0x80)
            {
                index += 1;
            }
            tokens.push(Token {
                kind: TokenKind::Word,
                text: &sql[start..index],
                range: start..index,
            });
        } else {
            index += 1;
            if let Some(text) = sql.get(start..index) {
                tokens.push(Token {
                    kind: TokenKind::Symbol,
                    text,
                    range: start..index,
                });
            }
        }
    }
    tokens
}

/// The statements of `sql`, split at top-level semicolons.
pub fn statements(sql: &str) -> Vec<Statement> {
    let tokens = tokenize(sql);
    let mut result = Vec::new();
    let mut start = 0;
    for index in 0..=tokens.len() {
        if index < tokens.len() && !tokens[index].is_symbol(";") {
            continue;
        }
        let part = &tokens[start..index];
        start = index + 1;
        let (Some(first), Some(last)) = (part.first(), part.last()) else {
            continue;
        };
        result.push(Statement {
            range: first.range.start..last.range.end,
            kind: classify(part),
        });
    }
    result
}

/// A table or index name at `tokens[*index]`, qualified names (`db.table`)
/// reduced to their last part. Empty when the text has no name there.
fn name(tokens: &[Token<'_>], index: &mut usize) -> String {
    let mut result = String::new();
    while let Some(token) = tokens.get(*index).filter(|token| token.is_name()) {
        token.text.clone_into(&mut result);
        *index += 1;
        if tokens.get(*index).is_some_and(|token| token.is_symbol(".")) {
            *index += 1;
        } else {
            break;
        }
    }
    result
}

/// Skips `words` when they follow in this order.
fn skip(tokens: &[Token<'_>], index: &mut usize, words: &[&str]) -> bool {
    let found = words.iter().enumerate().all(|(offset, word)| {
        tokens
            .get(*index + offset)
            .is_some_and(|token| token.is(word))
    });
    if found {
        *index += words.len();
    }
    found
}

fn classify(tokens: &[Token<'_>]) -> Kind {
    let mut index = 0;
    if skip(tokens, &mut index, &["CREATE"]) {
        let temporary = skip(tokens, &mut index, &["TEMPORARY"]);
        if skip(tokens, &mut index, &["TABLE"]) {
            let keywords = tokens[0].range.start..tokens[index - 1].range.end;
            let if_not_exists = skip(tokens, &mut index, &["IF", "NOT", "EXISTS"]);
            return Kind::CreateTable {
                table: name(tokens, &mut index),
                if_not_exists,
                temporary,
                keywords,
            };
        }
        if temporary {
            return Kind::Other;
        }
        let _ = skip(tokens, &mut index, &["UNIQUE"])
            || skip(tokens, &mut index, &["FULLTEXT"])
            || skip(tokens, &mut index, &["SPATIAL"]);
        if skip(tokens, &mut index, &["INDEX"]) {
            let if_not_exists = skip(tokens, &mut index, &["IF", "NOT", "EXISTS"]);
            let index_name = name(tokens, &mut index);
            while index < tokens.len() && !tokens[index].is("ON") {
                index += 1;
            }
            index += 1;
            return Kind::CreateIndex {
                table: name(tokens, &mut index),
                index: index_name,
                if_not_exists,
            };
        }
        return Kind::Other;
    }
    if skip(tokens, &mut index, &["ALTER"]) {
        let _ = skip(tokens, &mut index, &["ONLINE"]) || skip(tokens, &mut index, &["IGNORE"]);
        if !skip(tokens, &mut index, &["TABLE"]) {
            return Kind::Other;
        }
        let _ = skip(tokens, &mut index, &["IF", "EXISTS"]);
        let table = name(tokens, &mut index);
        return Kind::AlterTable {
            table,
            actions: actions(&tokens[index.min(tokens.len())..]),
        };
    }
    if skip(tokens, &mut index, &["DROP"]) {
        let _ = skip(tokens, &mut index, &["TEMPORARY"]);
        if skip(tokens, &mut index, &["TABLE"]) {
            let if_exists = skip(tokens, &mut index, &["IF", "EXISTS"]);
            let mut tables = Vec::new();
            loop {
                let table = name(tokens, &mut index);
                if table.is_empty() {
                    break;
                }
                tables.push(table);
                if !tokens.get(index).is_some_and(|token| token.is_symbol(",")) {
                    break;
                }
                index += 1;
            }
            return Kind::DropTable { tables, if_exists };
        }
        if skip(tokens, &mut index, &["INDEX"]) {
            let _ = skip(tokens, &mut index, &["IF", "EXISTS"]);
            let index_name = name(tokens, &mut index);
            let _ = skip(tokens, &mut index, &["ON"]);
            return Kind::DropIndex {
                table: name(tokens, &mut index),
                index: index_name,
            };
        }
        return Kind::Other;
    }
    if skip(tokens, &mut index, &["RENAME", "TABLE"]) {
        let mut tables = Vec::new();
        loop {
            let table = name(tokens, &mut index);
            if table.is_empty() {
                break;
            }
            tables.push(table);
            let _ = skip(tokens, &mut index, &["TO"]);
            let _ = name(tokens, &mut index);
            if !tokens.get(index).is_some_and(|token| token.is_symbol(",")) {
                break;
            }
            index += 1;
        }
        return Kind::RenameTable { tables };
    }
    let replace = skip(tokens, &mut index, &["REPLACE"]);
    if replace || skip(tokens, &mut index, &["INSERT"]) {
        let mut tolerant = replace;
        loop {
            if skip(tokens, &mut index, &["IGNORE"]) {
                tolerant = true;
            } else if !(skip(tokens, &mut index, &["LOW_PRIORITY"])
                || skip(tokens, &mut index, &["HIGH_PRIORITY"])
                || skip(tokens, &mut index, &["DELAYED"]))
            {
                break;
            }
        }
        if !skip(tokens, &mut index, &["INTO"]) && !tokens.get(index).is_some_and(Token::is_name) {
            return Kind::Other;
        }
        let table = name(tokens, &mut index);
        tolerant |= tokens[index.min(tokens.len())..]
            .windows(3)
            .any(|window| window[0].is("ON") && window[1].is("DUPLICATE") && window[2].is("KEY"));
        return Kind::Insert { table, tolerant };
    }
    Kind::Other
}

/// The parts of an `ALTER TABLE` after the table name, split at the commas
/// outside of parentheses.
fn actions(tokens: &[Token<'_>]) -> Vec<Action> {
    let mut result = Vec::new();
    let mut depth = 0usize;
    let mut start = 0;
    for index in 0..=tokens.len() {
        if let Some(token) = tokens.get(index) {
            if token.is_symbol("(") {
                depth += 1;
            } else if token.is_symbol(")") {
                depth = depth.saturating_sub(1);
            }
            if depth > 0 || !token.is_symbol(",") {
                continue;
            }
        }
        let part = &tokens[start..index];
        start = index + 1;
        let (Some(first), Some(last)) = (part.first(), part.last()) else {
            continue;
        };
        result.push(Action {
            range: first.range.start..last.range.end,
            kind: action(part),
        });
    }
    result
}

const KEY_KINDS: &[&str] = &[
    "INDEX",
    "KEY",
    "UNIQUE",
    "FULLTEXT",
    "SPATIAL",
    "CONSTRAINT",
    "FOREIGN",
    "PRIMARY",
];

fn action(tokens: &[Token<'_>]) -> ActionKind {
    let mut index = 0;
    let key = |tokens: &[Token<'_>], index: usize| {
        tokens
            .get(index)
            .is_some_and(|token| KEY_KINDS.iter().any(|kind| token.is(kind)))
    };
    if skip(tokens, &mut index, &["ADD"]) {
        if key(tokens, index) {
            let if_not_exists = tokens
                .windows(3)
                .any(|window| window[0].is("IF") && window[1].is("NOT") && window[2].is("EXISTS"));
            let mut at = index;
            return ActionKind::AddKey {
                name: add_key_name(tokens, &mut at),
                if_not_exists,
            };
        }
        let _ = skip(tokens, &mut index, &["COLUMN"]);
        let if_not_exists = skip(tokens, &mut index, &["IF", "NOT", "EXISTS"]);
        if tokens.get(index).is_some_and(|token| token.is_symbol("(")) {
            // `ADD (a INT, b INT)`: several columns at once.
            return ActionKind::Other;
        }
        let column = name(tokens, &mut index);
        let rest = &tokens[index.min(tokens.len())..];
        let has = |word: &str| rest.iter().any(|token| token.is(word));
        let not_null = rest
            .windows(2)
            .any(|window| window[0].is("NOT") && window[1].is("NULL"));
        return ActionKind::AddColumn {
            column,
            if_not_exists,
            required: not_null
                && !has("DEFAULT")
                && !has("GENERATED")
                && !has("AS")
                && !has("AUTO_INCREMENT"),
        };
    }
    if skip(tokens, &mut index, &["DROP"]) {
        if key(tokens, index) {
            let if_exists = tokens
                .windows(2)
                .any(|window| window[0].is("IF") && window[1].is("EXISTS"));
            while key(tokens, index) {
                index += 1;
            }
            let _ = skip(tokens, &mut index, &["IF", "EXISTS"]);
            return ActionKind::DropKey {
                name: name(tokens, &mut index),
                if_exists,
            };
        }
        let _ = skip(tokens, &mut index, &["COLUMN"]);
        let if_exists = skip(tokens, &mut index, &["IF", "EXISTS"]);
        return ActionKind::DropColumn {
            column: name(tokens, &mut index),
            if_exists,
        };
    }
    if skip(tokens, &mut index, &["RENAME"]) {
        if skip(tokens, &mut index, &["COLUMN"]) {
            let from = name(tokens, &mut index);
            let _ = skip(tokens, &mut index, &["TO"]);
            return ActionKind::RenameColumn {
                from,
                to: name(tokens, &mut index),
            };
        }
        if key(tokens, index) {
            return ActionKind::Other;
        }
        let _ = skip(tokens, &mut index, &["TO"]) || skip(tokens, &mut index, &["AS"]);
        return ActionKind::RenameTable {
            to: name(tokens, &mut index),
        };
    }
    if skip(tokens, &mut index, &["CHANGE"]) {
        let _ = skip(tokens, &mut index, &["COLUMN"]);
        let from = name(tokens, &mut index);
        let to = name(tokens, &mut index);
        if !from.is_empty() && !to.is_empty() && !from.eq_ignore_ascii_case(&to) {
            return ActionKind::RenameColumn { from, to };
        }
    }
    ActionKind::Other
}

/// The name an `ADD <key>` gives its key: `ADD INDEX name (…)`,
/// `ADD CONSTRAINT name FOREIGN KEY (…)`, `ADD UNIQUE KEY name (…)`.
fn add_key_name(tokens: &[Token<'_>], index: &mut usize) -> String {
    if skip(tokens, index, &["CONSTRAINT"]) {
        let _ = skip(tokens, index, &["IF", "NOT", "EXISTS"]);
        return name(tokens, index);
    }
    while tokens
        .get(*index)
        .is_some_and(|token| KEY_KINDS.iter().any(|kind| token.is(kind)))
    {
        *index += 1;
    }
    let _ = skip(tokens, index, &["IF", "NOT", "EXISTS"]);
    name(tokens, index)
}

#[cfg(test)]
mod tests {
    use super::*;

    fn kinds(sql: &str) -> Vec<Kind> {
        statements(sql)
            .into_iter()
            .map(|statement| statement.kind)
            .collect()
    }

    fn only_actions(sql: &str) -> Vec<ActionKind> {
        match kinds(sql).as_slice() {
            [Kind::AlterTable { actions, .. }] => {
                actions.iter().map(|action| action.kind.clone()).collect()
            }
            other => panic!("{other:?}"),
        }
    }

    #[test]
    fn schema_statements_are_classified() {
        let sql = "
            -- a comment; with a semicolon
            CREATE TABLE `swag_example` (`id` BINARY(16) NOT NULL, `name` VARCHAR(255) DEFAULT 'a;b');
            create table if not exists db.`swag_other` (id INT);
            CREATE TEMPORARY TABLE tmp (id INT);
            CREATE UNIQUE INDEX `idx.name` ON `swag_example` (`name`);
            DROP TABLE IF EXISTS `a`, b;
            DROP INDEX `idx.name` ON swag_example;
            RENAME TABLE `old` TO `new`;
            INSERT INTO `swag_example` (id) VALUES (1);
            INSERT IGNORE INTO swag_example VALUES (1);
            INSERT INTO swag_example (id) VALUES (1) ON DUPLICATE KEY UPDATE id = id;
            REPLACE INTO swag_example VALUES (1);
            SELECT 1 /* ; */;
            UPDATE swag_example SET name = 'CREATE TABLE x'
        ";
        let found = kinds(sql);
        assert_eq!(found.len(), 13);
        assert!(
            matches!(&found[0], Kind::CreateTable { table, if_not_exists: false, temporary: false, .. } if table == "swag_example")
        );
        assert!(
            matches!(&found[1], Kind::CreateTable { table, if_not_exists: true, .. } if table == "swag_other")
        );
        assert!(matches!(
            &found[2],
            Kind::CreateTable {
                temporary: true,
                ..
            }
        ));
        assert_eq!(
            found[3],
            Kind::CreateIndex {
                table: "swag_example".into(),
                index: "idx.name".into(),
                if_not_exists: false
            }
        );
        assert_eq!(
            found[4],
            Kind::DropTable {
                tables: vec!["a".into(), "b".into()],
                if_exists: true
            }
        );
        assert_eq!(
            found[5],
            Kind::DropIndex {
                table: "swag_example".into(),
                index: "idx.name".into()
            }
        );
        assert_eq!(
            found[6],
            Kind::RenameTable {
                tables: vec!["old".into()]
            }
        );
        let tolerant: Vec<bool> = found[7..11]
            .iter()
            .map(|kind| matches!(kind, Kind::Insert { table, tolerant: true } if table == "swag_example"))
            .collect();
        assert_eq!(tolerant, [false, true, true, true]);
        assert_eq!(&found[11..], [Kind::Other, Kind::Other]);

        // The keywords of a CREATE TABLE are where IF NOT EXISTS goes.
        let Kind::CreateTable { keywords, .. } = &kinds("  CREATE   TABLE a (id INT)")[0] else {
            panic!();
        };
        assert_eq!(
            &"  CREATE   TABLE a (id INT)"[keywords.clone()],
            "CREATE   TABLE"
        );
    }

    #[test]
    fn alter_table_actions_are_split_and_read() {
        assert_eq!(
            only_actions(
                "ALTER TABLE `product`
                    ADD COLUMN `swag_a` VARCHAR(255) NULL AFTER `name`,
                    ADD `swag_b` INT NOT NULL,
                    ADD COLUMN IF NOT EXISTS swag_c DECIMAL(10, 2) NOT NULL DEFAULT 0,
                    ADD swag_d INT GENERATED ALWAYS AS (swag_b + 1) STORED NOT NULL,
                    ADD INDEX `idx.a` (`swag_a`, `swag_b`),
                    ADD CONSTRAINT `fk.a` FOREIGN KEY (`swag_a`) REFERENCES `x` (`id`) ON DELETE CASCADE,
                    ADD UNIQUE KEY `uniq.b` (swag_b),
                    DROP COLUMN `old`,
                    DROP IF EXISTS older,
                    DROP INDEX `idx.old`,
                    DROP FOREIGN KEY `fk.old`,
                    DROP PRIMARY KEY,
                    RENAME COLUMN `x` TO `y`,
                    CHANGE `same` `same` INT NULL,
                    CHANGE COLUMN `from` `to` INT NULL,
                    RENAME TO `product_new`,
                    MODIFY COLUMN `swag_a` TEXT NULL,
                    ENGINE = InnoDB"
            ),
            [
                ActionKind::AddColumn { column: "swag_a".into(), if_not_exists: false, required: false },
                ActionKind::AddColumn { column: "swag_b".into(), if_not_exists: false, required: true },
                ActionKind::AddColumn { column: "swag_c".into(), if_not_exists: true, required: false },
                ActionKind::AddColumn { column: "swag_d".into(), if_not_exists: false, required: false },
                ActionKind::AddKey { name: "idx.a".into(), if_not_exists: false },
                ActionKind::AddKey { name: "fk.a".into(), if_not_exists: false },
                ActionKind::AddKey { name: "uniq.b".into(), if_not_exists: false },
                ActionKind::DropColumn { column: "old".into(), if_exists: false },
                ActionKind::DropColumn { column: "older".into(), if_exists: true },
                ActionKind::DropKey { name: "idx.old".into(), if_exists: false },
                ActionKind::DropKey { name: "fk.old".into(), if_exists: false },
                ActionKind::DropKey { name: String::new(), if_exists: false },
                ActionKind::RenameColumn { from: "x".into(), to: "y".into() },
                ActionKind::Other,
                ActionKind::RenameColumn { from: "from".into(), to: "to".into() },
                ActionKind::RenameTable { to: "product_new".into() },
                ActionKind::Other,
                ActionKind::Other,
            ]
        );
    }

    #[test]
    fn text_that_is_no_complete_statement_claims_nothing() {
        for sql in [
            "",
            "   ",
            "ALTER TABLE `",
            "ALTER TABLE",
            "CREATE",
            "CREATE TABLE",
            "DROP",
            "INSERT",
            "hello world",
            "ALTER TABLE `%s` ADD",
            "'unterminated",
            "`unterminated",
            "/* unterminated",
            "ALTER TABLE a ADD (",
            "é ü ; ö",
        ] {
            for statement in statements(sql) {
                assert!(statement.range.end <= sql.len(), "{sql}");
                assert!(sql.get(statement.range.clone()).is_some(), "{sql}");
            }
        }
        // A table name from a placeholder is a name; one that is missing is empty.
        assert!(
            matches!(&kinds("ALTER TABLE `%s` ADD COLUMN a INT")[0], Kind::AlterTable { table, .. } if table == "%s")
        );
        assert!(
            matches!(&kinds("ALTER TABLE")[0], Kind::AlterTable { table, actions } if table.is_empty() && actions.is_empty())
        );
        assert_eq!(
            kinds("ALTER TABLE a ADD"),
            [Kind::AlterTable {
                table: "a".into(),
                actions: vec![Action {
                    range: 14..17,
                    kind: ActionKind::AddColumn {
                        column: String::new(),
                        if_not_exists: false,
                        required: false
                    }
                }]
            }]
        );
    }
}
