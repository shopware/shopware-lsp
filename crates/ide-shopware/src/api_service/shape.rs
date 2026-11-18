//! The JSON shape of a request or response, and its TypeScript type.

/// What a JSON value looks like, as far as the controller tells.
#[derive(Clone, Debug, PartialEq, Eq)]
pub(crate) enum Shape {
    /// Nothing is known: `unknown`.
    Unknown,
    /// No content.
    Void,
    Null,
    String,
    Number,
    Boolean,
    /// A file download.
    Blob,
    Array(Box<Shape>),
    /// An object with any keys.
    Record(Box<Shape>),
    /// An object with known keys; `open` when further keys may exist.
    Object {
        fields: Vec<Field>,
        open: bool,
    },
    Union(Vec<Shape>),
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub(crate) struct Field {
    pub name: String,
    pub shape: Shape,
    pub optional: bool,
}

impl Shape {
    /// The union of `shapes`: nested unions are flattened and duplicates
    /// dropped. Anything joined with `unknown` is `unknown`.
    pub(crate) fn union(shapes: impl IntoIterator<Item = Shape>) -> Shape {
        let mut members: Vec<Shape> = Vec::new();
        let mut pending: Vec<Shape> = shapes.into_iter().collect();
        pending.reverse();
        while let Some(shape) = pending.pop() {
            match shape {
                Shape::Unknown => return Shape::Unknown,
                Shape::Union(inner) => pending.extend(inner.into_iter().rev()),
                shape if members.contains(&shape) => {}
                shape => members.push(shape),
            }
        }
        // `string | null` reads better than `null | string`.
        for last in [Shape::Null, Shape::Void] {
            if let Some(position) = members.iter().position(|member| *member == last) {
                let member = members.remove(position);
                members.push(member);
            }
        }
        match members.len() {
            0 => Shape::Unknown,
            1 => members.remove(0),
            _ => Shape::Union(members),
        }
    }

    /// The TypeScript type, with nested object members indented below
    /// `indent`.
    pub(crate) fn typescript(&self, indent: usize) -> String {
        match self {
            Shape::Unknown => "unknown".to_owned(),
            Shape::Void => "void".to_owned(),
            Shape::Null => "null".to_owned(),
            Shape::String => "string".to_owned(),
            Shape::Number => "number".to_owned(),
            Shape::Boolean => "boolean".to_owned(),
            Shape::Blob => "Blob".to_owned(),
            Shape::Array(item) => {
                let item_type = item.typescript(indent);
                if matches!(**item, Shape::Union(_)) {
                    format!("({item_type})[]")
                } else {
                    format!("{item_type}[]")
                }
            }
            Shape::Record(value) => format!("Record<string, {}>", value.typescript(indent)),
            Shape::Object { fields, open } => object_type(fields, *open, indent),
            Shape::Union(members) => members
                .iter()
                .map(|member| member.typescript(indent))
                .collect::<Vec<_>>()
                .join(" | "),
        }
    }
}

fn object_type(fields: &[Field], open: bool, indent: usize) -> String {
    if fields.is_empty() {
        return if open {
            "Record<string, unknown>".to_owned()
        } else {
            "Record<string, never>".to_owned()
        };
    }
    let inner = "    ".repeat(indent + 1);
    let mut text = String::from("{\n");
    for field in fields {
        let optional = if field.optional { "?" } else { "" };
        text.push_str(&format!(
            "{inner}{}{optional}: {};\n",
            property_key(&field.name),
            field.shape.typescript(indent + 1)
        ));
    }
    if open {
        text.push_str(&format!("{inner}[key: string]: unknown;\n"));
    }
    text.push_str(&"    ".repeat(indent));
    text.push('}');
    text
}

/// `name` as an object key: quoted unless it is an identifier.
pub(crate) fn property_key(name: &str) -> String {
    if is_identifier(name) {
        name.to_owned()
    } else {
        format!("'{}'", name.replace('\\', "\\\\").replace('\'', "\\'"))
    }
}

pub(crate) fn is_identifier(name: &str) -> bool {
    let mut bytes = name.bytes();
    bytes
        .next()
        .is_some_and(|first| first.is_ascii_alphabetic() || first == b'_' || first == b'$')
        && bytes.all(|byte| byte.is_ascii_alphanumeric() || byte == b'_' || byte == b'$')
}
