use std::fmt;

/// Identifies one semantic type constructor. Discriminants match Go's
/// `types.Kind` values.
#[derive(Clone, Copy, Debug, PartialEq, Eq, PartialOrd, Ord, Hash)]
#[repr(u8)]
pub enum Kind {
    Unknown = 0,
    Error,
    Never,
    Mixed,
    Void,
    Null,
    Bool,
    True,
    False,
    Int,
    Float,
    String,
    Object,
    Resource,
    ArrayKey,
    Array,
    NonEmptyArray,
    List,
    NonEmptyList,
    Iterable,
    Callable,
    ClassString,
    Template,
    SelfType,
    Static,
    Parent,
    LiteralInt,
    LiteralFloat,
    LiteralString,
    ArrayShape,
    ObjectShape,
    Union,
    Intersection,
    Conditional,
}

impl Kind {
    pub(crate) const ALL: [Kind; 34] = [
        Kind::Unknown,
        Kind::Error,
        Kind::Never,
        Kind::Mixed,
        Kind::Void,
        Kind::Null,
        Kind::Bool,
        Kind::True,
        Kind::False,
        Kind::Int,
        Kind::Float,
        Kind::String,
        Kind::Object,
        Kind::Resource,
        Kind::ArrayKey,
        Kind::Array,
        Kind::NonEmptyArray,
        Kind::List,
        Kind::NonEmptyList,
        Kind::Iterable,
        Kind::Callable,
        Kind::ClassString,
        Kind::Template,
        Kind::SelfType,
        Kind::Static,
        Kind::Parent,
        Kind::LiteralInt,
        Kind::LiteralFloat,
        Kind::LiteralString,
        Kind::ArrayShape,
        Kind::ObjectShape,
        Kind::Union,
        Kind::Intersection,
        Kind::Conditional,
    ];

    pub(crate) const fn from_u8(value: u8) -> Kind {
        Kind::ALL[value as usize]
    }

    /// The Go `Kind.String()` text.
    pub const fn as_str(self) -> &'static str {
        match self {
            Kind::Unknown => "unknown",
            Kind::Error => "error",
            Kind::Never => "never",
            Kind::Mixed => "mixed",
            Kind::Void => "void",
            Kind::Null => "null",
            Kind::Bool => "bool",
            Kind::True => "true",
            Kind::False => "false",
            Kind::Int => "int",
            Kind::Float => "float",
            Kind::String => "string",
            Kind::Object => "object",
            Kind::Resource => "resource",
            Kind::ArrayKey => "array-key",
            Kind::Array => "array",
            Kind::NonEmptyArray => "non-empty-array",
            Kind::List => "list",
            Kind::NonEmptyList => "non-empty-list",
            Kind::Iterable => "iterable",
            Kind::Callable => "callable",
            Kind::ClassString => "class-string",
            Kind::Template => "template",
            Kind::SelfType => "self",
            Kind::Static => "static",
            Kind::Parent => "parent",
            Kind::LiteralInt => "literal-int",
            Kind::LiteralFloat => "literal-float",
            Kind::LiteralString => "literal-string",
            Kind::ArrayShape => "array-shape",
            Kind::ObjectShape => "object-shape",
            Kind::Union => "union",
            Kind::Intersection => "intersection",
            Kind::Conditional => "conditional",
        }
    }

    /// Named and literal kinds carry a semantic name instead of cached text.
    pub(crate) const fn has_semantic_name(self) -> bool {
        matches!(
            self,
            Kind::Object
                | Kind::Template
                | Kind::LiteralInt
                | Kind::LiteralFloat
                | Kind::LiteralString
        )
    }
}

impl fmt::Display for Kind {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter.write_str(self.as_str())
    }
}
