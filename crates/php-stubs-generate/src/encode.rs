//! MessagePack encoding identical to the Go generator
//! (`github.com/vmihailenco/msgpack/v5` with the catalog struct tags).
//!
//! Go encodes typed integers with their fixed width (`uint8` as `0xcc`,
//! `uint16` as `0xcd`, `uint32` as `0xce`), catalog structs as arrays and
//! shared semantic structs as maps keyed by Go field name (including the
//! zeroed `Range` fields).

use swlsp_php_semantic::{
    Attribute, AttributeValue, CallContract, CallValue, ExpectedArgumentContract, Flags, SymbolKind,
};
use swlsp_php_stubs::catalog::{Catalog, Parameter, Symbol};
use swlsp_php_types::Type;

/// How lists that Go may hold as `nil` or as an empty slice are written.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub enum EmptyListPolicy {
    /// The committed `phpstorm-stubs.msgpack`: class-like declarations keep
    /// empty `extends` (classes, traits and enums), `implements`, and
    /// hierarchy type lists, and non-synthetic methods and functions keep an
    /// empty `throws` list.
    #[default]
    Committed,
    /// The current Go generator: those lists are `nil` when empty.
    GoHead,
}

#[derive(Default)]
struct Writer {
    data: Vec<u8>,
}

impl Writer {
    fn nil(&mut self) {
        self.data.push(0xc0);
    }

    fn bool(&mut self, value: bool) {
        self.data.push(if value { 0xc3 } else { 0xc2 });
    }

    fn u8(&mut self, value: u8) {
        self.data.extend_from_slice(&[0xcc, value]);
    }

    fn u16(&mut self, value: u16) {
        self.data.push(0xcd);
        self.data.extend_from_slice(&value.to_be_bytes());
    }

    fn u32(&mut self, value: u32) {
        self.data.push(0xce);
        self.data.extend_from_slice(&value.to_be_bytes());
    }

    fn header(&mut self, length: usize, fix: Option<(u8, usize)>, codes: [Option<u8>; 3]) {
        if let Some((fix, fix_max)) = fix
            && length <= fix_max
        {
            self.data.push(fix | length as u8);
        } else if let Some(code) = codes[0]
            && length <= 0xff
        {
            self.data.extend_from_slice(&[code, length as u8]);
        } else if length <= 0xffff {
            self.data.push(codes[1].unwrap_or_default());
            self.data.extend_from_slice(&(length as u16).to_be_bytes());
        } else {
            self.data.push(codes[2].unwrap_or_default());
            self.data.extend_from_slice(&(length as u32).to_be_bytes());
        }
    }

    fn str(&mut self, value: &str) {
        self.header(
            value.len(),
            Some((0xa0, 31)),
            [Some(0xd9), Some(0xda), Some(0xdb)],
        );
        self.data.extend_from_slice(value.as_bytes());
    }

    fn bin(&mut self, value: &[u8]) {
        self.header(value.len(), None, [Some(0xc4), Some(0xc5), Some(0xc6)]);
        self.data.extend_from_slice(value);
    }

    fn array(&mut self, length: usize) {
        self.header(length, Some((0x90, 15)), [None, Some(0xdc), Some(0xdd)]);
    }

    fn map(&mut self, length: usize) {
        self.header(length, Some((0x80, 15)), [None, Some(0xde), Some(0xdf)]);
    }

    fn ty(&mut self, value: &Type) {
        self.str(&value.persisted_string());
    }

    /// A Go slice: `nil` when empty unless `keep_empty`.
    fn list<T>(&mut self, items: &[T], keep_empty: bool, mut encode: impl FnMut(&mut Self, &T)) {
        if items.is_empty() && !keep_empty {
            self.nil();
            return;
        }
        self.array(items.len());
        for item in items {
            encode(self, item);
        }
    }
}

/// Encodes a packed catalog exactly as Go `catalog.Encode`.
pub fn encode(catalog: &Catalog, policy: EmptyListPolicy) -> Vec<u8> {
    let mut writer = Writer::default();
    writer.array(9);
    writer.u8(catalog.format);
    writer.str(&catalog.repository);
    writer.str(&catalog.commit);
    writer.list(&catalog.versions, false, |writer, version| {
        writer.array(2);
        writer.u8(version.major);
        writer.u8(version.minor);
    });
    writer.list(&catalog.symbols, false, |writer, symbol| {
        symbol_record(writer, symbol, policy);
    });
    writer.list(&catalog.contracts, false, contract);
    writer.list(&catalog.contract_extensions, false, |writer, value| {
        writer.str(value);
    });
    writer.list(&catalog.bundles, false, |writer, bundle| {
        writer.array(2);
        writer.str(&bundle.extension);
        writer.bin(&bundle.data);
    });
    writer.list(&catalog.extension_symbols, false, |writer, symbol| {
        writer.array(2);
        writer.str(&symbol.name);
        writer.str(&symbol.extension);
    });
    writer.data
}

/// The encoding of a one-symbol catalog that Go uses as the cross-version
/// identity of a record.
pub fn encode_symbol_record(symbol: &Symbol, policy: EmptyListPolicy) -> Vec<u8> {
    let mut writer = Writer::default();
    symbol_record(&mut writer, symbol, policy);
    writer.data
}

pub(crate) fn encode_bundle_payload(
    symbols: &[Symbol],
    contracts: &[CallContract],
    policy: EmptyListPolicy,
) -> Vec<u8> {
    let mut writer = Writer::default();
    writer.array(2);
    writer.list(symbols, false, |writer, symbol| {
        symbol_record(writer, symbol, policy);
    });
    writer.list(contracts, false, contract);
    writer.data
}

fn symbol_record(writer: &mut Writer, symbol: &Symbol, policy: EmptyListPolicy) {
    let committed = policy == EmptyListPolicy::Committed;
    let class_like = symbol.kind.is_class_like();
    let keep_extends = committed
        && matches!(
            symbol.kind,
            SymbolKind::Class | SymbolKind::Trait | SymbolKind::Enum
        );
    let keep_hierarchy = committed && class_like;
    let keep_throws = committed
        && matches!(symbol.kind, SymbolKind::Method | SymbolKind::Function)
        && !symbol.flags.has(Flags::SYNTHETIC);

    writer.array(25);
    writer.u16(symbol.version_mask);
    writer.str(&symbol.extension);
    writer.u8(symbol.kind as u8);
    writer.str(&symbol.name);
    writer.str(&symbol.fully_qualified);
    writer.str(&symbol.container);
    writer.u8(symbol.visibility as u8);
    writer.u8(symbol.write_visibility as u8);
    writer.bool(symbol.has_write_visibility);
    writer.u32(symbol.flags.0);
    writer.ty(&symbol.ty);
    writer.ty(&symbol.native_type);
    writer.ty(&symbol.doc_type);
    writer.ty(&symbol.return_type);
    // Go allocates both slices with make, so they are never nil.
    writer.list(&symbol.parameters, true, parameter);
    writer.list(&symbol.templates, true, |writer, template| {
        writer.array(5);
        writer.str(&template.name);
        writer.ty(&template.bound);
        writer.ty(&template.default);
        writer.bool(template.covariant);
        writer.bool(template.contravariant);
    });
    writer.list(&symbol.extends, keep_extends, |writer, value| {
        writer.str(value)
    });
    writer.list(&symbol.implements, keep_hierarchy, |writer, value| {
        writer.str(value);
    });
    writer.list(&symbol.traits, false, |writer, value| writer.str(value));
    writer.list(&symbol.extends_types, keep_hierarchy, Writer::ty);
    writer.list(&symbol.implements_types, keep_hierarchy, Writer::ty);
    writer.list(&symbol.trait_types, keep_hierarchy, Writer::ty);
    writer.list(&symbol.throws, keep_throws, Writer::ty);
    writer.list(&symbol.attributes, false, attribute);
    writer.str(&symbol.doc_summary);
}

fn parameter(writer: &mut Writer, parameter: &Parameter) {
    writer.array(9);
    writer.str(&parameter.name);
    writer.ty(&parameter.ty);
    writer.ty(&parameter.native_type);
    writer.ty(&parameter.doc_type);
    writer.list(&parameter.assistant_tags, false, |writer, value| {
        writer.str(value);
    });
    writer.list(&parameter.attributes, false, attribute);
    match &parameter.default_value {
        Some(value) => attribute_value(writer, value),
        None => writer.nil(),
    }
    writer.u32(parameter.flags.0);
    writer.bool(parameter.optional);
}

fn text_range(writer: &mut Writer, start: u32, end: u32) {
    writer.map(2);
    writer.str("Start");
    writer.u32(start);
    writer.str("End");
    writer.u32(end);
}

fn attribute(writer: &mut Writer, attribute: &Attribute) {
    writer.map(3);
    writer.str("Name");
    writer.str(&attribute.name);
    writer.str("Arguments");
    writer.list(&attribute.arguments, false, |writer, argument| {
        writer.map(3);
        writer.str("Name");
        writer.str(&argument.name);
        writer.str("Value");
        attribute_value(writer, &argument.value);
        writer.str("Range");
        text_range(
            writer,
            u32::from(argument.range.start()),
            u32::from(argument.range.end()),
        );
    });
    writer.str("Range");
    text_range(
        writer,
        u32::from(attribute.range.start()),
        u32::from(attribute.range.end()),
    );
}

fn attribute_value(writer: &mut Writer, value: &AttributeValue) {
    writer.map(4);
    writer.str("Kind");
    writer.u8(value.kind as u8);
    writer.str("Value");
    writer.str(&value.value);
    writer.str("Expression");
    writer.str(&value.expression);
    writer.str("Items");
    writer.list(&value.items, false, |writer, item| {
        writer.map(3);
        writer.str("Key");
        attribute_value(writer, &item.key);
        writer.str("HasKey");
        writer.bool(item.has_key);
        writer.str("Value");
        attribute_value(writer, &item.value);
    });
}

fn contract(writer: &mut Writer, contract: &CallContract) {
    writer.map(6);
    writer.str("Target");
    writer.map(3);
    writer.str("Kind");
    writer.u8(contract.target.kind as u8);
    writer.str("Name");
    writer.str(&contract.target.name);
    writer.str("Class");
    writer.str(&contract.target.class);
    writer.str("Return");
    writer.map(3);
    writer.str("Kind");
    writer.u8(contract.return_contract.kind as u8);
    writer.str("Argument");
    writer.u16(contract.return_contract.argument);
    writer.str("Map");
    writer.list(&contract.return_contract.map, false, |writer, entry| {
        writer.map(2);
        writer.str("Key");
        call_value(writer, &entry.key);
        writer.str("Result");
        call_value(writer, &entry.result);
    });
    writer.str("ExpectedArguments");
    writer.list(&contract.expected_arguments, false, expected_argument);
    writer.str("ExpectedReturnValues");
    writer.list(&contract.expected_return_values, false, call_value);
    writer.str("ExitPoint");
    writer.bool(contract.exit_point);
    writer.str("ExitArguments");
    writer.list(&contract.exit_arguments, false, expected_argument);
}

fn expected_argument(writer: &mut Writer, expected: &ExpectedArgumentContract) {
    writer.map(2);
    writer.str("Argument");
    writer.u16(expected.argument);
    writer.str("Values");
    writer.list(&expected.values, false, call_value);
}

fn call_value(writer: &mut Writer, value: &CallValue) {
    writer.map(3);
    writer.str("Kind");
    writer.u8(value.kind as u8);
    writer.str("Value");
    writer.str(&value.value);
    writer.str("Expression");
    writer.str(&value.expression);
}
