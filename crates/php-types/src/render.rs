//! Canonical type rendering. Output is byte-identical to Go's `Type.String()`.
//! Composite children without cached text (an empty shape, a parameterless
//! callable with a non-mixed result, or an uncached union) are rendered in
//! full when nested, so the canonical text round-trips through the parser.

use std::borrow::Cow;

use crate::gostr::write_go_quote;
use crate::kind::Kind;
use crate::ty::{CallableParameter, Type};

/// Renders a type at the top level (Go's `Type.render`).
pub(crate) fn render(value: &Type) -> String {
    match value.kind() {
        Kind::Unknown
        | Kind::Error
        | Kind::Never
        | Kind::Mixed
        | Kind::Void
        | Kind::Null
        | Kind::Bool
        | Kind::True
        | Kind::False
        | Kind::Int
        | Kind::Float
        | Kind::String
        | Kind::Resource
        | Kind::ArrayKey => value.kind().as_str().to_owned(),
        Kind::SelfType | Kind::Static | Kind::Parent => {
            render_generic(value.kind().as_str(), value.arguments())
        }
        Kind::LiteralString => crate::gostr::go_quote(value.name()),
        Kind::ClassString => {
            let arguments = value.arguments();
            if arguments.is_empty() || arguments[0] == Type::OBJECT {
                return "class-string".to_owned();
            }
            render_generic("class-string", arguments)
        }
        Kind::Object => {
            if value.name().is_empty() {
                return "object".to_owned();
            }
            render_generic(value.name(), value.arguments())
        }
        Kind::LiteralInt | Kind::LiteralFloat | Kind::Template => value.name().to_owned(),
        Kind::Array | Kind::NonEmptyArray | Kind::List | Kind::NonEmptyList | Kind::Iterable => {
            render_container(value)
        }
        Kind::Callable => render_callable(value),
        Kind::Conditional => render_conditional(value),
        Kind::ArrayShape | Kind::ObjectShape => render_shape(value),
        Kind::Union => render_composite(value.arguments(), "|", true),
        Kind::Intersection => render_composite(value.arguments(), "&", false),
    }
}

fn render_container(value: &Type) -> String {
    let name = value.kind().as_str();
    let arguments = value.arguments();
    match value.kind() {
        Kind::List | Kind::NonEmptyList => {
            if arguments.is_empty() || arguments[0].kind() == Kind::Mixed {
                return name.to_owned();
            }
        }
        _ => {
            if arguments.len() != 2
                || arguments[0].kind() == Kind::Mixed && arguments[1].kind() == Kind::Mixed
            {
                return name.to_owned();
            }
        }
    }
    render_generic(name, arguments)
}

fn render_callable(value: &Type) -> String {
    let parameters = value.parameters();
    if parameters.is_empty() && value.result().kind() == Kind::Mixed {
        return "callable".to_owned();
    }
    let mut out = String::with_capacity(callable_rendered_length(parameters, value.result()));
    out.push_str("callable(");
    for (index, parameter) in parameters.iter().enumerate() {
        if index > 0 {
            out.push(',');
        }
        write_callable_parameter(&mut out, parameter);
    }
    out.push_str("):");
    write_rendered(&mut out, value.result());
    out
}

fn callable_rendered_length(parameters: &[CallableParameter], result: &Type) -> usize {
    let mut length = "callable():".len() + rendered_length_hint(result);
    for (index, parameter) in parameters.iter().enumerate() {
        if index > 0 {
            length += 1;
        }
        length += usize::from(parameter.by_reference) + 3 * usize::from(parameter.variadic);
        length += rendered_length_hint(&parameter.ty);
        if !parameter.name.is_empty() {
            length += 1 + parameter.name.len();
        }
        length += usize::from(parameter.optional);
    }
    length
}

fn write_callable_parameter(out: &mut String, parameter: &CallableParameter) {
    if parameter.by_reference {
        out.push('&');
    }
    if parameter.variadic {
        out.push_str("...");
    }
    write_rendered(out, &parameter.ty);
    if !parameter.name.is_empty() {
        out.push(' ');
        out.push_str(&parameter.name);
    }
    if parameter.optional {
        out.push('=');
    }
}

fn render_conditional(value: &Type) -> String {
    let arguments = value.arguments();
    if arguments.len() != 4 {
        return "conditional".to_owned();
    }
    let length = "( is  ?  : )".len() + arguments.iter().map(rendered_length_hint).sum::<usize>();
    let mut out = String::with_capacity(length);
    out.push('(');
    write_rendered(&mut out, &arguments[0]);
    out.push_str(" is ");
    write_rendered(&mut out, &arguments[1]);
    out.push_str(" ? ");
    write_rendered(&mut out, &arguments[2]);
    out.push_str(" : ");
    write_rendered(&mut out, &arguments[3]);
    out.push(')');
    out
}

fn render_shape(value: &Type) -> String {
    let prefix = if value.kind() == Kind::ObjectShape {
        "object"
    } else {
        "array"
    };
    let fields = value.fields();
    let mut length = prefix.len() + 2;
    for (index, field) in fields.iter().enumerate() {
        length += usize::from(index > 0) + field.name.len() + 1 + rendered_length_hint(&field.ty);
        length += usize::from(field.optional);
    }
    if value.is_open_shape() {
        length += usize::from(!fields.is_empty()) + 3;
    }
    let mut out = String::with_capacity(length);
    out.push_str(prefix);
    out.push('{');
    for (index, field) in fields.iter().enumerate() {
        if index > 0 {
            out.push(',');
        }
        out.push_str(&shape_field_name(&field.name));
        if field.optional {
            out.push('?');
        }
        out.push(':');
        write_rendered(&mut out, &field.ty);
    }
    if value.is_open_shape() {
        if !fields.is_empty() {
            out.push(',');
        }
        out.push_str("...");
    }
    out.push('}');
    out
}

/// Returns a shape key as rendered: already-quoted keys and plain type-name
/// keys stay verbatim, every other key is Go-quoted.
pub(crate) fn shape_field_name(name: &str) -> Cow<'_, str> {
    let bytes = name.as_bytes();
    if bytes.len() >= 2
        && (bytes[0] == b'\'' && bytes[bytes.len() - 1] == b'\''
            || bytes[0] == b'"' && bytes[bytes.len() - 1] == b'"')
    {
        return Cow::Borrowed(name);
    }
    if !name.is_empty() && bytes.iter().all(|&byte| is_type_name_byte(byte)) {
        return Cow::Borrowed(name);
    }
    Cow::Owned(crate::gostr::go_quote(name))
}

pub(crate) fn is_type_name_byte(value: u8) -> bool {
    value >= 0x80 || value.is_ascii_alphanumeric() || matches!(value, b'_' | b'\\' | b'-' | b'$')
}

fn render_generic(name: &str, arguments: &[Type]) -> String {
    if arguments.is_empty() {
        return name.to_owned();
    }
    let length = name.len() + 2 + arguments.len() - 1
        + arguments.iter().map(rendered_length_hint).sum::<usize>();
    let mut out = String::with_capacity(length);
    out.push_str(name);
    write_arguments(&mut out, arguments);
    out
}

fn write_arguments(out: &mut String, arguments: &[Type]) {
    out.push('<');
    for (index, argument) in arguments.iter().enumerate() {
        if index > 0 {
            out.push(',');
        }
        write_rendered(out, argument);
    }
    out.push('>');
}

fn wraps_in_union(value: &Type) -> bool {
    matches!(
        value.kind(),
        Kind::Intersection | Kind::Callable | Kind::Conditional
    )
}

fn render_composite(values: &[Type], separator: &str, wrap_intersection: bool) -> String {
    let mut length = values.len().saturating_sub(1) * separator.len();
    for value in values {
        length += rendered_length_hint(value);
        if wrap_intersection && wraps_in_union(value) {
            length += 2;
        }
    }
    let mut out = String::with_capacity(length);
    for (index, value) in values.iter().enumerate() {
        if index > 0 {
            out.push_str(separator);
        }
        let wrap = wrap_intersection && wraps_in_union(value);
        if wrap {
            out.push('(');
        }
        write_rendered(&mut out, value);
        if wrap {
            out.push(')');
        }
    }
    out
}

/// Exact length for cached composites and a cheap lower bound otherwise.
pub(crate) fn rendered_length_hint(value: &Type) -> usize {
    if let Some(text) = value.cached_text() {
        return text.len();
    }
    match value.kind() {
        Kind::Object | Kind::SelfType | Kind::Static | Kind::Parent => {
            let name = if value.kind() != Kind::Object {
                value.kind().as_str()
            } else if value.name().is_empty() {
                return "object".len();
            } else {
                value.name()
            };
            let arguments = value.arguments();
            if arguments.is_empty() {
                return name.len();
            }
            name.len() + 2 + arguments.len() - 1
                + arguments.iter().map(rendered_length_hint).sum::<usize>()
        }
        Kind::LiteralInt | Kind::LiteralFloat | Kind::Template => value.name().len(),
        Kind::LiteralString => value.name().len() + 2,
        Kind::ArrayShape => "array{}".len(),
        Kind::ObjectShape => "object{}".len(),
        kind => kind.as_str().len(),
    }
}

/// Writes a nested type (Go's `writeRenderedType`).
pub(crate) fn write_rendered(out: &mut String, value: &Type) {
    if let Some(text) = value.cached_text() {
        out.push_str(text);
        return;
    }
    match value.kind() {
        Kind::Object | Kind::SelfType | Kind::Static | Kind::Parent => {
            let name = if value.kind() != Kind::Object {
                value.kind().as_str()
            } else if value.name().is_empty() {
                out.push_str("object");
                return;
            } else {
                value.name()
            };
            out.push_str(name);
            let arguments = value.arguments();
            if !arguments.is_empty() {
                write_arguments(out, arguments);
            }
        }
        Kind::LiteralInt | Kind::LiteralFloat | Kind::Template => out.push_str(value.name()),
        Kind::LiteralString => write_go_quote(out, value.name()),
        Kind::Unknown
        | Kind::Error
        | Kind::Never
        | Kind::Mixed
        | Kind::Void
        | Kind::Null
        | Kind::Bool
        | Kind::True
        | Kind::False
        | Kind::Int
        | Kind::Float
        | Kind::String
        | Kind::Resource
        | Kind::ArrayKey => out.push_str(value.kind().as_str()),
        // Uncached composites (`callable():T`, `array{}`, uncached unions)
        // must render in full; their kind name would not parse back.
        _ => out.push_str(&render(value)),
    }
}
