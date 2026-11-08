//! Dumps every materialized stub declaration and call contract in the text
//! format of the Go differential dumper, for oracle comparison.

use std::fmt::Write as _;
use std::io::{BufWriter, Write};

use swlsp_php_project::Version;
use swlsp_php_semantic::{
    Attribute, AttributeValue, CallContract, CallValue, Document, ExpectedArgumentContract, Str,
};
use swlsp_php_types::{Type, go_quote as q};

fn ty(value: &Type) -> String {
    q(&value.text())
}

fn tys(values: &[Type]) -> String {
    format!("[{}]", values.iter().map(ty).collect::<Vec<_>>().join(","))
}

fn strs(values: &[Str]) -> String {
    format!(
        "[{}]",
        values
            .iter()
            .map(|value| q(value))
            .collect::<Vec<_>>()
            .join(",")
    )
}

fn value(value_: &AttributeValue) -> String {
    let mut out = format!(
        "{}:{}:{}[",
        value_.kind as u8,
        q(&value_.value),
        q(&value_.expression)
    );
    for (index, item) in value_.items.iter().enumerate() {
        if index > 0 {
            out.push(',');
        }
        let _ = write!(
            out,
            "{}={}>{}",
            value(&item.key),
            item.has_key,
            value(&item.value)
        );
    }
    out.push(']');
    out
}

fn attrs(attributes: &[Attribute]) -> String {
    let mut out = String::from("[");
    for (index, attribute) in attributes.iter().enumerate() {
        if index > 0 {
            out.push(',');
        }
        out.push_str(&q(&attribute.name));
        out.push('(');
        for (argument_index, argument) in attribute.arguments.iter().enumerate() {
            if argument_index > 0 {
                out.push(',');
            }
            let _ = write!(out, "{}={}", q(&argument.name), value(&argument.value));
        }
        out.push(')');
    }
    out.push(']');
    out
}

fn cv(value: &CallValue) -> String {
    format!(
        "{}:{}:{}",
        value.kind as u8,
        q(&value.value),
        q(&value.expression)
    )
}

fn cvs(values: &[CallValue]) -> String {
    format!("[{}]", values.iter().map(cv).collect::<Vec<_>>().join(","))
}

fn expected(values: &[ExpectedArgumentContract]) -> String {
    format!(
        "[{}]",
        values
            .iter()
            .map(|value| format!("{}{}", value.argument, cvs(&value.values)))
            .collect::<Vec<_>>()
            .join(",")
    )
}

fn contract(contract: &CallContract) -> String {
    let map: Vec<String> = contract
        .return_contract
        .map
        .iter()
        .map(|entry| format!("{}=>{}", cv(&entry.key), cv(&entry.result)))
        .collect();
    format!(
        "C {}:{}:{} ret={}:{}:[{}] exp={} rv={} exit={}:{}",
        contract.target.kind as u8,
        q(&contract.target.name),
        q(&contract.target.class),
        contract.return_contract.kind as u8,
        contract.return_contract.argument,
        map.join(","),
        expected(&contract.expected_arguments),
        cvs(&contract.expected_return_values),
        contract.exit_point,
        expected(&contract.exit_arguments)
    )
}

fn dump(out: &mut impl Write, document: &Document) -> std::io::Result<()> {
    writeln!(
        out,
        "D {} {} {}",
        q(&document.path),
        document.symbols.len(),
        document.call_contracts.len()
    )?;
    for symbol in &document.symbols {
        writeln!(
            out,
            "S {}|{}|{}|{}|{}|{}|{}|{}|{}|{}|{}|{}|{}|{}|{}",
            q(&symbol.id),
            symbol.kind as u8,
            q(&symbol.name),
            q(&symbol.fully_qualified),
            q(&symbol.container),
            q(&symbol.path),
            symbol.visibility as u8,
            symbol.write_visibility as u8,
            symbol.has_write_visibility,
            symbol.flags.0,
            ty(&symbol.ty),
            ty(&symbol.native_type),
            ty(&symbol.doc_type),
            ty(&symbol.return_type),
            q(symbol.doc_summary())
        )?;
        for parameter in &symbol.parameters {
            let default = parameter
                .default_value
                .as_deref()
                .map_or_else(|| "nil".to_owned(), value);
            writeln!(
                out,
                "  P {}|{}|{}|{}|{}|{}|{}|{}|{}|{}",
                q(&parameter.id),
                q(&parameter.name),
                ty(&parameter.ty),
                ty(&parameter.native_type),
                ty(&parameter.doc_type),
                strs(&parameter.assistant_tags),
                parameter.flags.0,
                parameter.optional,
                default,
                attrs(&parameter.attributes)
            )?;
        }
        for template in symbol.templates() {
            writeln!(
                out,
                "  T {}|{}|{}|{}|{}",
                q(&template.name),
                ty(&template.bound),
                ty(&template.default),
                template.covariant,
                template.contravariant
            )?;
        }
        writeln!(
            out,
            "  H {} {} {} {} {} {} throws={}",
            strs(symbol.extends()),
            strs(symbol.implements()),
            strs(symbol.traits()),
            tys(symbol.extends_types()),
            tys(symbol.implements_types()),
            tys(symbol.trait_types()),
            tys(symbol.throws())
        )?;
        writeln!(out, "  A {}", attrs(symbol.attributes()))?;
    }
    for value in &document.call_contracts {
        writeln!(out, "{}", contract(value))?;
    }
    Ok(())
}

fn main() -> std::io::Result<()> {
    let stdout = std::io::stdout();
    let mut out = BufWriter::with_capacity(1 << 20, stdout.lock());
    let empty: [&str; 0] = [];
    for (major, minor) in [
        (7, 3),
        (7, 4),
        (8, 0),
        (8, 1),
        (8, 2),
        (8, 3),
        (8, 4),
        (8, 5),
        (8, 6),
        (9, 0),
    ] {
        let version = Version::new(major, minor, 0);
        dump(&mut out, &swlsp_php_stubs::document(version))?;
        dump(
            &mut out,
            &swlsp_php_stubs::document_for_extensions(version, &empty, &[]),
        )?;
        dump(
            &mut out,
            &swlsp_php_stubs::document_for_extensions(
                version,
                &[
                    "curl",
                    "redis",
                    "dom",
                    "xsl",
                    "pdo_sqlite",
                    "intl",
                    "ext-mbstring",
                ],
                &[&["libxml"]],
            ),
        )?;
    }
    for name in [
        "DOMDocument",
        "curl_init",
        "\\DOMNode::appendChild",
        "redis::get",
        "strlen",
        "nope",
        "Random\\Randomizer",
    ] {
        let extension = swlsp_php_stubs::extension_for_symbol(name);
        writeln!(
            out,
            "E {} {} {}",
            q(name),
            q(extension.unwrap_or("")),
            extension.is_some()
        )?;
    }
    out.flush()
}
