//! Versioned binary cache format of a workspace graph.
//!
//! The format is private to the Rust implementation and not compatible with
//! Go's MessagePack layout. Integers are LEB128 varints. Identifier-like
//! strings and types are written once into per-graph tables and referenced
//! by index (0 means empty/unknown); free text is written inline. Each
//! symbol's signature/metadata payload is length-prefixed so summary
//! decoding can skip it without parsing.

use std::fmt;
use std::sync::{Arc, OnceLock};

use rustc_hash::FxHashMap;
use swlsp_php_types::Type;

use crate::str::Str;
use swlsp_syntax::TextRange;

use crate::atom::{Atom, empty_name};
use crate::call_contract::{
    CallContract, CallMapEntry, CallReturnContract, CallReturnKind, CallTarget, CallTargetKind,
    CallValue, CallValueKind, ExpectedArgumentContract,
};
use crate::document::NameKind;
use crate::packed::{
    LazyDetails, PackedBuilder, PackedCore, PackedDocument, PackedId, PackedName, PackedParameter,
    ParameterExtras, ParameterTypeSource, SymbolDetail,
};
use crate::references::{PackedReferences, ReferenceRecord};
use crate::symbol::{
    Attribute, AttributeArgument, AttributeArrayItem, AttributeValue, AttributeValueKind,
    ConstantArrayItem, ConstantReturn, Flags, LiteralReturn, Shared, SignatureExtras,
    SymbolHierarchy, SymbolId, SymbolKind, SymbolMetadata, TemplateParameter, TraitAlias,
    TypeAssertion, Visibility,
};
use crate::workspace::{WorkspaceGraphDecoder, WorkspaceGraphLoader};

const FORMAT_VERSION: u8 = 1;
const MAX_COLLECTION: u64 = 1 << 24;
const MAX_STRING: u64 = 128 << 20;
const MAX_ATTRIBUTE_DEPTH: usize = 64;

/// A malformed or incompatible cache payload.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct DecodeError(String);

impl fmt::Display for DecodeError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(f, "decode workspace graph: {}", self.0)
    }
}

impl std::error::Error for DecodeError {}

fn error<T>(message: impl Into<String>) -> Result<T, DecodeError> {
    Err(DecodeError(message.into()))
}

// -- writer ---------------------------------------------------------------------

#[derive(Default)]
struct Writer {
    out: Vec<u8>,
}

impl Writer {
    fn u8(&mut self, value: u8) {
        self.out.push(value);
    }

    fn bool(&mut self, value: bool) {
        self.out.push(u8::from(value));
    }

    fn uint(&mut self, mut value: u64) {
        while value >= 0x80 {
            self.out.push((value as u8) | 0x80);
            value >>= 7;
        }
        self.out.push(value as u8);
    }

    fn int(&mut self, value: i64) {
        self.uint(((value << 1) ^ (value >> 63)) as u64);
    }

    fn len(&mut self, value: usize) {
        self.uint(value as u64);
    }

    fn text(&mut self, value: &str) {
        self.len(value.len());
        self.out.extend_from_slice(value.as_bytes());
    }

    fn range(&mut self, value: TextRange) {
        let start: u32 = value.start().into();
        let end: u32 = value.end().into();
        self.uint(u64::from(start));
        self.uint(u64::from(end - start));
    }
}

/// Per-graph string and type tables. Strings are keyed by value so that
/// encoding never interns.
#[derive(Default)]
struct Tables {
    strings: Vec<Str>,
    string_index: FxHashMap<Str, u64>,
    types: Vec<Type>,
    type_index: FxHashMap<Type, u64>,
}

impl Tables {
    fn string(&mut self, value: &Str) -> u64 {
        if value.is_empty() {
            return 0;
        }
        if let Some(index) = self.string_index.get(value.as_str()) {
            return *index;
        }
        self.strings.push(value.clone());
        let index = self.strings.len() as u64;
        self.string_index.insert(value.clone(), index);
        index
    }

    fn atom(&mut self, atom: Atom) -> u64 {
        self.string(&atom.name())
    }

    fn ty(&mut self, value: &Type) -> u64 {
        if value.is_unknown() {
            return 0;
        }
        if let Some(index) = self.type_index.get(value) {
            return *index;
        }
        self.types.push(value.clone());
        let index = self.types.len() as u64;
        self.type_index.insert(value.clone(), index);
        index
    }
}

struct Encoder<'t> {
    w: Writer,
    tables: &'t mut Tables,
}

impl Encoder<'_> {
    fn string(&mut self, value: &Str) {
        let index = self.tables.string(value);
        self.w.uint(index);
    }

    fn atom(&mut self, atom: Atom) {
        let index = self.tables.atom(atom);
        self.w.uint(index);
    }

    fn ty(&mut self, value: &Type) {
        let index = self.tables.ty(value);
        self.w.uint(index);
    }

    fn strings(&mut self, values: &[Str]) {
        self.w.len(values.len());
        for value in values {
            self.string(value);
        }
    }

    fn types(&mut self, values: &[Type]) {
        self.w.len(values.len());
        for value in values {
            self.ty(value);
        }
    }

    fn hierarchy(&mut self, hierarchy: &SymbolHierarchy) {
        self.strings(&hierarchy.extends);
        self.strings(&hierarchy.implements);
        self.strings(&hierarchy.traits);
        self.types(&hierarchy.extends_types);
        self.types(&hierarchy.implements_types);
        self.types(&hierarchy.trait_types);
        self.w.len(hierarchy.aliases.len());
        for alias in &hierarchy.aliases {
            self.string(&alias.trait_name);
            self.string(&alias.method);
            self.string(&alias.alias);
            self.w.u8(alias.visibility as u8);
            self.w.bool(alias.has_visibility);
        }
    }

    fn attribute_value(&mut self, value: &AttributeValue) {
        self.w.u8(value.kind as u8);
        self.w.text(&value.value);
        self.w.text(&value.expression);
        self.w.len(value.items.len());
        for item in &value.items {
            self.w.bool(item.has_key);
            self.attribute_value(&item.key);
            self.attribute_value(&item.value);
        }
    }

    fn attributes(&mut self, attributes: &[Attribute]) {
        self.w.len(attributes.len());
        for attribute in attributes {
            self.string(&attribute.name);
            self.w.range(attribute.range);
            self.w.len(attribute.arguments.len());
            for argument in &attribute.arguments {
                self.string(&argument.name);
                self.w.range(argument.range);
                self.attribute_value(&argument.value);
            }
        }
    }

    fn detail(&mut self, detail: &SymbolDetail) {
        self.w.len(detail.parameters.len());
        for parameter in detail.parameters.iter() {
            self.parameter(parameter);
        }
        match detail.signature.get() {
            None => self.w.bool(false),
            Some(signature) => {
                self.w.bool(true);
                self.w.len(signature.templates.len());
                for template in &signature.templates {
                    self.string(&template.name);
                    self.ty(&template.bound);
                    self.ty(&template.default);
                    self.w
                        .u8(u8::from(template.covariant) | u8::from(template.contravariant) << 1);
                }
                self.types(&signature.throws);
                self.w.len(signature.assertions.len());
                for assertion in &signature.assertions {
                    self.string(&assertion.target);
                    self.ty(&assertion.ty);
                    self.w.u8(u8::from(assertion.when_true)
                        | u8::from(assertion.conditional) << 1
                        | u8::from(assertion.negated) << 2);
                }
                self.w.len(signature.literal_returns.len());
                for literal in &signature.literal_returns {
                    self.w.text(&literal.value);
                    self.w.range(literal.range);
                    self.ty(&literal.ty);
                }
                self.w.len(signature.constant_returns.len());
                for constant in &signature.constant_returns {
                    self.string(&constant.receiver);
                    self.string(&constant.name);
                    self.w.range(constant.range);
                }
            }
        }
        match detail.metadata.get() {
            None => self.w.bool(false),
            Some(metadata) => {
                self.w.bool(true);
                self.attributes(&metadata.attributes);
                self.w.len(metadata.constant_array.len());
                for item in &metadata.constant_array {
                    self.w.text(&item.key);
                    self.w.range(item.key_range);
                    self.w.text(&item.value);
                    self.w.range(item.value_range);
                    self.ty(&item.ty);
                }
                self.w.text(&metadata.doc_summary);
            }
        }
    }

    fn parameter(&mut self, parameter: &PackedParameter) {
        self.atom(parameter.name);
        self.ty(&parameter.native_type);
        self.ty(&parameter.doc_type);
        for range in parameter.ranges() {
            self.w.range(range);
        }
        self.w.uint(u64::from(parameter.flags));
        self.w
            .u8(parameter.source as u8 | u8::from(parameter.optional) << 2);
        match &parameter.extras {
            None => self.w.bool(false),
            Some(extras) => {
                self.w.bool(true);
                match &extras.id {
                    None => self.w.bool(false),
                    Some(id) => {
                        self.w.bool(true);
                        self.w.text(id);
                    }
                }
                self.ty(&extras.ty);
                self.strings(&extras.assistant_tags);
                self.attributes(&extras.attributes);
                match &extras.default_value {
                    None => self.w.bool(false),
                    Some(value) => {
                        self.w.bool(true);
                        self.attribute_value(value);
                    }
                }
            }
        }
    }

    fn call_value(&mut self, value: &CallValue) {
        self.w.u8(value.kind as u8);
        self.w.text(&value.value);
        self.w.text(&value.expression);
    }

    fn expected(&mut self, contracts: &[ExpectedArgumentContract]) {
        self.w.len(contracts.len());
        for contract in contracts {
            self.w.uint(u64::from(contract.argument));
            self.w.len(contract.values.len());
            for value in &contract.values {
                self.call_value(value);
            }
        }
    }

    fn call_contract(&mut self, contract: &CallContract) {
        self.w.u8(contract.target.kind as u8);
        self.w.text(&contract.target.name);
        self.w.text(&contract.target.class);
        self.w.u8(contract.return_contract.kind as u8);
        self.w.uint(u64::from(contract.return_contract.argument));
        self.w.len(contract.return_contract.map.len());
        for entry in &contract.return_contract.map {
            self.call_value(&entry.key);
            self.call_value(&entry.result);
        }
        self.expected(&contract.expected_arguments);
        self.w.len(contract.expected_return_values.len());
        for value in &contract.expected_return_values {
            self.call_value(value);
        }
        self.w.bool(contract.exit_point);
        self.expected(&contract.exit_arguments);
    }
}

/// Encodes `document`. Lazy summaries encode their loaded details.
pub(crate) fn encode(document: &PackedDocument) -> Vec<u8> {
    let mut tables = Tables::default();
    let mut body = Encoder {
        w: Writer::default(),
        tables: &mut tables,
    };
    body.w.len(document.symbols.len());
    for index in 0..document.symbols.len() as u32 {
        let symbol = document.symbol(index);
        let id = symbol.id_value();
        let id_index = body.tables.string(&id);
        body.w.uint(id_index);
        body.atom(symbol.name);
        let fully_qualified = symbol.fully_qualified_value();
        let fully_qualified_index = body.tables.string(&fully_qualified);
        body.w.uint(fully_qualified_index);
        body.atom(symbol.container);
        for range in document.ranges(index) {
            body.w.range(range);
        }
        for value in document.types(index) {
            body.ty(&value);
        }
        body.w.uint(u64::from(symbol.flags().0));
        body.w.u8(symbol.kind() as u8);
        body.w
            .u8(symbol.visibility() as u8 | (symbol.write_visibility() as u8) << 2);
        body.w.bool(symbol.has_write_visibility());
        match &symbol.hierarchy {
            None => body.w.bool(false),
            Some(hierarchy) => {
                body.w.bool(true);
                body.hierarchy(hierarchy);
            }
        }
        match document.detail(index) {
            None => body.w.len(0),
            Some(detail) => {
                let mut nested = Encoder {
                    w: Writer::default(),
                    tables: body.tables,
                };
                nested.detail(&detail);
                let bytes = nested.w.out;
                body.w.len(bytes.len());
                body.w.out.extend_from_slice(&bytes);
            }
        }
    }
    let references = &document.references;
    body.w.len(references.len());
    for record in references.records() {
        body.w.range(record.range);
        body.atom(references.string_atom(record.name));
        body.atom(references.string_atom(record.resolved));
        body.ty(references.ty(record.receiver));
        body.w.uint(u64::from(record.value_start));
        body.w.uint(u64::from(record.qualified));
        body.w.uint(u64::from(record.candidates));
        body.w.u8(record.kind as u8);
        body.w.u8(record.target_kind as u8);
        body.w.u8(record.flags as u8);
    }
    let values = references.value_indexes();
    body.w.len(values.len());
    for &value in values {
        body.atom(references.string_atom(value));
    }
    body.w.len(document.call_contracts.len());
    for contract in document.call_contracts.iter() {
        body.call_contract(contract);
    }
    let body = body.w.out;

    let mut head = Writer::default();
    head.u8(FORMAT_VERSION);
    head.text(&document.path);
    head.int(document.version);
    head.uint(document.workspace_revision);
    head.text(&document.namespace);
    head.len(tables.strings.len());
    for value in &tables.strings {
        head.text(value);
    }
    head.len(tables.types.len());
    for value in &tables.types {
        head.text(&value.persisted_string());
    }
    head.out.extend_from_slice(&body);
    head.out
}

// -- reader ---------------------------------------------------------------------

struct Reader<'a> {
    bytes: &'a [u8],
    position: usize,
}

impl<'a> Reader<'a> {
    fn u8(&mut self) -> Result<u8, DecodeError> {
        let Some(&value) = self.bytes.get(self.position) else {
            return error("unexpected end of input");
        };
        self.position += 1;
        Ok(value)
    }

    fn bool(&mut self) -> Result<bool, DecodeError> {
        match self.u8()? {
            0 => Ok(false),
            1 => Ok(true),
            other => error(format!("invalid boolean {other}")),
        }
    }

    fn uint(&mut self) -> Result<u64, DecodeError> {
        let mut result = 0u64;
        let mut shift = 0u32;
        loop {
            let byte = self.u8()?;
            if shift == 63 && byte > 1 {
                return error("varint overflow");
            }
            result |= u64::from(byte & 0x7F) << shift;
            if byte & 0x80 == 0 {
                return Ok(result);
            }
            shift += 7;
            if shift > 63 {
                return error("varint overflow");
            }
        }
    }

    fn u32(&mut self) -> Result<u32, DecodeError> {
        u32::try_from(self.uint()?).or_else(|_| error("value exceeds u32"))
    }

    fn u16(&mut self) -> Result<u16, DecodeError> {
        u16::try_from(self.uint()?).or_else(|_| error("value exceeds u16"))
    }

    fn int(&mut self) -> Result<i64, DecodeError> {
        let value = self.uint()?;
        Ok(((value >> 1) as i64) ^ -((value & 1) as i64))
    }

    fn len(&mut self, what: &str) -> Result<usize, DecodeError> {
        let value = self.uint()?;
        if value > MAX_COLLECTION {
            return error(format!("{what} length {value} exceeds {MAX_COLLECTION}"));
        }
        Ok(value as usize)
    }

    fn bytes(&mut self, length: usize) -> Result<&'a [u8], DecodeError> {
        let end = self
            .position
            .checked_add(length)
            .filter(|&end| end <= self.bytes.len());
        let Some(end) = end else {
            return error("unexpected end of input");
        };
        let slice = &self.bytes[self.position..end];
        self.position = end;
        Ok(slice)
    }

    fn text(&mut self) -> Result<&'a str, DecodeError> {
        let length = self.uint()?;
        if length > MAX_STRING {
            return error(format!("string length {length} exceeds {MAX_STRING}"));
        }
        let bytes = self.bytes(length as usize)?;
        std::str::from_utf8(bytes).or_else(|_| error("invalid UTF-8"))
    }

    fn range(&mut self) -> Result<TextRange, DecodeError> {
        let start = self.u32()?;
        let length = self.u32()?;
        let Some(end) = start.checked_add(length) else {
            return error("range overflow");
        };
        Ok(TextRange::new(start.into(), end.into()))
    }
}

struct Decoder<'a, 'd> {
    r: Reader<'a>,
    /// The graph's string table; entries are interned on first use, so
    /// derivable IDs and skipped lazy details never enter the atom table.
    strings: Vec<(&'a str, Option<Atom>)>,
    types: &'a [Type],
    context: &'d mut WorkspaceGraphDecoder,
}

impl<'a> Decoder<'a, '_> {
    /// The string-table index of the next value (0 is the empty string).
    fn string_index(&mut self) -> Result<usize, DecodeError> {
        let index = self.r.uint()?;
        if index as usize > self.strings.len() {
            return error(format!("string index {index} out of range"));
        }
        Ok(index as usize)
    }

    fn string_text(&self, index: usize) -> &'a str {
        if index == 0 {
            ""
        } else {
            self.strings[index - 1].0
        }
    }

    fn intern(&mut self, index: usize) -> Atom {
        if index == 0 {
            return Atom::EMPTY;
        }
        let (text, atom) = &mut self.strings[index - 1];
        *atom.get_or_insert_with(|| Atom::intern(text))
    }

    fn atom(&mut self) -> Result<Atom, DecodeError> {
        let index = self.string_index()?;
        Ok(self.intern(index))
    }

    fn name(&mut self) -> Result<Str, DecodeError> {
        Ok(self.atom()?.name())
    }

    fn text(&mut self) -> Result<Str, DecodeError> {
        let value = self.r.text()?;
        if value.is_empty() {
            return Ok(empty_name());
        }
        Ok(self.context.canon.text(&Str::from(value)))
    }

    fn ty(&mut self) -> Result<Type, DecodeError> {
        let index = self.r.uint()?;
        if index == 0 {
            return Ok(Type::unknown());
        }
        match self.types.get(index as usize - 1) {
            Some(value) => Ok(value.clone()),
            None => error(format!("type index {index} out of range")),
        }
    }

    fn names(&mut self) -> Result<Vec<Str>, DecodeError> {
        let length = self.r.len("names")?;
        (0..length).map(|_| self.name()).collect()
    }

    fn types(&mut self) -> Result<Vec<Type>, DecodeError> {
        let length = self.r.len("types")?;
        (0..length).map(|_| self.ty()).collect()
    }

    fn visibility(&mut self, value: u8) -> Result<Visibility, DecodeError> {
        Visibility::from_u8(value).map_or_else(|| error(format!("invalid visibility {value}")), Ok)
    }

    fn hierarchy(&mut self) -> Result<SymbolHierarchy, DecodeError> {
        let extends = self.names()?;
        let implements = self.names()?;
        let traits = self.names()?;
        let extends_types = self.types()?;
        let implements_types = self.types()?;
        let trait_types = self.types()?;
        let length = self.r.len("trait aliases")?;
        let mut aliases = Vec::with_capacity(length);
        for _ in 0..length {
            let trait_name = self.name()?;
            let method = self.name()?;
            let alias = self.name()?;
            let visibility = self.r.u8()?;
            aliases.push(TraitAlias {
                trait_name,
                method,
                alias,
                visibility: self.visibility(visibility)?,
                has_visibility: self.r.bool()?,
            });
        }
        Ok(SymbolHierarchy {
            extends,
            implements,
            traits,
            extends_types,
            implements_types,
            trait_types,
            aliases,
        })
    }

    fn attribute_value(&mut self, depth: usize) -> Result<AttributeValue, DecodeError> {
        if depth > MAX_ATTRIBUTE_DEPTH {
            return error("attribute value nesting too deep");
        }
        let kind = self.r.u8()?;
        let Some(kind) = AttributeValueKind::from_u8(kind) else {
            return error(format!("invalid attribute value kind {kind}"));
        };
        let value = self.text()?;
        let expression = self.text()?;
        let length = self.r.len("attribute array items")?;
        let mut items = Vec::with_capacity(length);
        for _ in 0..length {
            let has_key = self.r.bool()?;
            let key = self.attribute_value(depth + 1)?;
            let value = self.attribute_value(depth + 1)?;
            items.push(AttributeArrayItem {
                key,
                has_key,
                value,
            });
        }
        Ok(AttributeValue {
            kind,
            value,
            expression,
            items,
        })
    }

    fn attributes(&mut self) -> Result<Vec<Attribute>, DecodeError> {
        let length = self.r.len("attributes")?;
        let mut attributes = Vec::with_capacity(length);
        for _ in 0..length {
            let name = self.name()?;
            let range = self.r.range()?;
            let count = self.r.len("attribute arguments")?;
            let mut arguments = Vec::with_capacity(count);
            for _ in 0..count {
                let name = self.name()?;
                let range = self.r.range()?;
                let value = self.attribute_value(0)?;
                arguments.push(AttributeArgument { name, value, range });
            }
            attributes.push(Attribute {
                name,
                arguments,
                range,
            });
        }
        Ok(attributes)
    }

    fn parameter(&mut self) -> Result<PackedParameter, DecodeError> {
        let name = self.atom()?;
        let native_type = self.ty()?;
        let doc_type = self.ty()?;
        let ranges = [self.r.range()?, self.r.range()?, self.r.range()?];
        let flags = self.r.u16()?;
        let packed = self.r.u8()?;
        let source = match packed & 0x3 {
            0 => ParameterTypeSource::Native,
            1 => ParameterTypeSource::Doc,
            2 => ParameterTypeSource::Explicit,
            other => return error(format!("invalid parameter type source {other}")),
        };
        let extras = if self.r.bool()? {
            let id = if self.r.bool()? {
                Some(SymbolId::from(self.r.text()?))
            } else {
                None
            };
            let ty = self.ty()?;
            let assistant_tags = self.names()?;
            let attributes = self.attributes()?;
            let default_value = if self.r.bool()? {
                Some(Box::new(self.attribute_value(0)?))
            } else {
                None
            };
            ParameterExtras {
                id,
                ranges: None,
                ty,
                assistant_tags,
                attributes,
                default_value,
            }
        } else {
            ParameterExtras::default()
        };
        Ok(PackedParameter::new(
            name,
            native_type,
            doc_type,
            ranges,
            extras,
            flags,
            source,
            packed & 0x4 != 0,
        ))
    }

    fn detail(&mut self) -> Result<SymbolDetail, DecodeError> {
        let length = self.r.len("parameters")?;
        let parameters = (0..length)
            .map(|_| self.parameter())
            .collect::<Result<Vec<_>, _>>()?;
        let signature = if self.r.bool()? {
            let count = self.r.len("templates")?;
            let mut templates = Vec::with_capacity(count);
            for _ in 0..count {
                let name = self.name()?;
                let bound = self.ty()?;
                let default = self.ty()?;
                let variance = self.r.u8()?;
                templates.push(TemplateParameter {
                    name,
                    bound,
                    default,
                    covariant: variance & 1 != 0,
                    contravariant: variance & 2 != 0,
                });
            }
            let throws = self.types()?;
            let count = self.r.len("assertions")?;
            let mut assertions = Vec::with_capacity(count);
            for _ in 0..count {
                let target = self.name()?;
                let ty = self.ty()?;
                let bits = self.r.u8()?;
                assertions.push(TypeAssertion {
                    target,
                    ty,
                    when_true: bits & 1 != 0,
                    conditional: bits & 2 != 0,
                    negated: bits & 4 != 0,
                });
            }
            let count = self.r.len("literal returns")?;
            let mut literal_returns = Vec::with_capacity(count);
            for _ in 0..count {
                let value = self.text()?;
                let range = self.r.range()?;
                literal_returns.push(LiteralReturn {
                    value,
                    range,
                    ty: self.ty()?,
                });
            }
            let count = self.r.len("constant returns")?;
            let mut constant_returns = Vec::with_capacity(count);
            for _ in 0..count {
                let receiver = self.name()?;
                let name = self.name()?;
                constant_returns.push(ConstantReturn {
                    receiver,
                    name,
                    range: self.r.range()?,
                });
            }
            Shared::<SignatureExtras>::new(SignatureExtras {
                templates,
                throws,
                assertions,
                literal_returns,
                constant_returns,
            })
        } else {
            Shared::default()
        };
        let metadata = if self.r.bool()? {
            let attributes = self.attributes()?;
            let count = self.r.len("constant array")?;
            let mut constant_array = Vec::with_capacity(count);
            for _ in 0..count {
                let key = self.text()?;
                let key_range = self.r.range()?;
                let value = self.text()?;
                let value_range = self.r.range()?;
                constant_array.push(ConstantArrayItem {
                    key,
                    key_range,
                    value,
                    value_range,
                    ty: self.ty()?,
                });
            }
            let doc_summary = self.text()?;
            Shared::<SymbolMetadata>::new(SymbolMetadata {
                attributes,
                constant_array,
                doc_summary,
            })
        } else {
            Shared::default()
        };
        Ok(SymbolDetail {
            parameters: parameters.into_boxed_slice(),
            signature,
            metadata,
        })
    }

    fn call_value(&mut self) -> Result<CallValue, DecodeError> {
        let kind = match self.r.u8()? {
            0 => CallValueKind::Expression,
            1 => CallValueKind::String,
            2 => CallValueKind::Number,
            3 => CallValueKind::Constant,
            4 => CallValueKind::ClassConstant,
            other => return error(format!("invalid call value kind {other}")),
        };
        Ok(CallValue {
            kind,
            value: self.text()?,
            expression: self.text()?,
        })
    }

    fn call_values(&mut self) -> Result<Vec<CallValue>, DecodeError> {
        let length = self.r.len("call values")?;
        (0..length).map(|_| self.call_value()).collect()
    }

    fn expected(&mut self) -> Result<Vec<ExpectedArgumentContract>, DecodeError> {
        let length = self.r.len("expected arguments")?;
        let mut result = Vec::with_capacity(length);
        for _ in 0..length {
            let argument = self.r.u16()?;
            result.push(ExpectedArgumentContract {
                argument,
                values: self.call_values()?,
            });
        }
        Ok(result)
    }

    fn call_contract(&mut self) -> Result<CallContract, DecodeError> {
        let kind = match self.r.u8()? {
            0 => CallTargetKind::Function,
            1 => CallTargetKind::Method,
            other => return error(format!("invalid call target kind {other}")),
        };
        let name = self.text()?;
        let class = self.text()?;
        let return_kind = match self.r.u8()? {
            0 => CallReturnKind::Unknown,
            1 => CallReturnKind::ArgumentType,
            2 => CallReturnKind::ArgumentElementType,
            3 => CallReturnKind::ArgumentMap,
            other => return error(format!("invalid call return kind {other}")),
        };
        let argument = self.r.u16()?;
        let length = self.r.len("call map")?;
        let mut map = Vec::with_capacity(length);
        for _ in 0..length {
            let key = self.call_value()?;
            map.push(CallMapEntry {
                key,
                result: self.call_value()?,
            });
        }
        Ok(CallContract {
            target: CallTarget { kind, name, class },
            return_contract: CallReturnContract {
                kind: return_kind,
                argument,
                map,
            },
            expected_arguments: self.expected()?,
            expected_return_values: self.call_values()?,
            exit_point: self.r.bool()?,
            exit_arguments: self.expected()?,
        })
    }
}

/// Decodes a graph. With a loader, signature/metadata payloads are skipped
/// and loaded on demand.
pub(crate) fn decode(
    bytes: &[u8],
    context: &mut WorkspaceGraphDecoder,
    loader: Option<Arc<dyn WorkspaceGraphLoader>>,
) -> Result<PackedDocument, DecodeError> {
    // Every atom of one document lives in one generation.
    let generation = crate::atom::target_generation();
    crate::atom::with_generation(&generation.clone(), || {
        decode_in(bytes, context, loader, generation)
    })
}

fn decode_in(
    bytes: &[u8],
    context: &mut WorkspaceGraphDecoder,
    loader: Option<Arc<dyn WorkspaceGraphLoader>>,
    generation: Arc<crate::atom::Generation>,
) -> Result<PackedDocument, DecodeError> {
    let mut reader = Reader { bytes, position: 0 };
    let version = reader.u8()?;
    if version != FORMAT_VERSION {
        return error(format!("unsupported format version {version}"));
    }
    let path = context.canon.identifier(&Str::from(reader.text()?));
    let document_version = reader.int()?;
    let workspace_revision = reader.uint()?;
    let namespace = context.canon.identifier(&Str::from(reader.text()?));
    let count = reader.len("strings")?;
    let mut strings = Vec::with_capacity(count);
    for _ in 0..count {
        strings.push((reader.text()?, None));
    }
    let count = reader.len("types")?;
    let mut types = Vec::with_capacity(count);
    for _ in 0..count {
        let text = reader.text()?;
        let value = match context.type_cache.get(text) {
            Some(value) => value.clone(),
            None => {
                let parsed = swlsp_php_types::parse_persisted(text).map_err(|parse| {
                    DecodeError(format!("parse persisted PHP type {text:?}: {parse}"))
                })?;
                let canonical = context.canon.ty(&parsed);
                context.type_cache.insert(text.into(), canonical.clone());
                canonical
            }
        };
        types.push(value);
    }
    let mut decoder = Decoder {
        r: reader,
        strings,
        types: &types,
        context,
    };
    let count = decoder.r.len("symbols")?;
    let mut builder = PackedBuilder::default();
    builder.symbols.reserve(count);
    let mut any_lazy = false;
    for _ in 0..count {
        let id = decoder.string_index()?;
        let name = decoder.atom()?;
        let fully_qualified = decoder.string_index()?;
        let container = decoder.atom()?;
        let ranges = [decoder.r.range()?, decoder.r.range()?, decoder.r.range()?];
        let types = [decoder.ty()?, decoder.ty()?, decoder.ty()?, decoder.ty()?];
        let flags = decoder.r.u32()?;
        if flags >> 14 != 0 {
            return error(format!("invalid symbol flags {flags:#x}"));
        }
        let kind = decoder.r.u8()?;
        let Some(kind) = SymbolKind::from_u8(kind) else {
            return error(format!("invalid symbol kind {kind}"));
        };
        let visibilities = decoder.r.u8()?;
        let visibility = decoder.visibility(visibilities & 0x3)?;
        let write_visibility = decoder.visibility(visibilities >> 2)?;
        let has_write_visibility = decoder.r.bool()?;
        let hierarchy = if decoder.r.bool()? {
            Shared::<SymbolHierarchy>::new(decoder.hierarchy()?)
                .arc()
                .cloned()
        } else {
            None
        };
        let detail_length = decoder.r.len("symbol detail")?;
        let mut detail = None;
        let mut lazy_detail = false;
        if detail_length > 0 {
            let start = decoder.r.position;
            if loader.is_some() {
                decoder.r.bytes(detail_length)?;
                lazy_detail = true;
                any_lazy = true;
            } else {
                detail = Some(Arc::new(decoder.detail()?));
                if decoder.r.position != start + detail_length {
                    return error("symbol detail length mismatch");
                }
            }
        }
        let id_text = decoder.string_text(id);
        let fully_qualified_text = decoder.string_text(fully_qualified);
        let id = if PackedId::derivable(id_text, kind, fully_qualified_text) {
            PackedId::Derived
        } else {
            PackedId::Stored(decoder.intern(id))
        };
        let fully_qualified = match PackedName::member_owner(
            fully_qualified_text,
            kind,
            name.as_str(),
            container.as_str(),
        ) {
            Some(owner) => PackedName::Member(Atom::intern(owner)),
            None => PackedName::Stored(decoder.intern(fully_qualified)),
        };
        builder.push(PackedCore {
            id,
            name,
            fully_qualified,
            container,
            ranges,
            types,
            kind,
            flags: Flags(flags),
            visibility,
            write_visibility,
            has_write_visibility,
            hierarchy,
            detail,
            lazy_detail,
        });
    }
    let count = decoder.r.len("references")?;
    let mut records = Vec::with_capacity(count);
    let mut reference_strings: Vec<Atom> = Vec::new();
    let mut reference_string_index: FxHashMap<Atom, u32> = FxHashMap::default();
    let mut local_string = |atom: Atom| -> u32 {
        if atom.is_empty() {
            return 0;
        }
        *reference_string_index.entry(atom).or_insert_with(|| {
            reference_strings.push(atom);
            reference_strings.len() as u32
        })
    };
    let mut reference_types: Vec<Type> = Vec::new();
    let mut reference_type_index: FxHashMap<Type, u32> = FxHashMap::default();
    for _ in 0..count {
        let range = decoder.r.range()?;
        let name = local_string(decoder.atom()?);
        let resolved = local_string(decoder.atom()?);
        let receiver_type = decoder.ty()?;
        let receiver = if receiver_type.is_unknown() {
            0
        } else {
            *reference_type_index
                .entry(receiver_type.clone())
                .or_insert_with(|| {
                    reference_types.push(receiver_type);
                    reference_types.len() as u32
                })
        };
        let value_start = decoder.r.u32()?;
        let qualified = decoder.r.u32()?;
        let candidates = decoder.r.u32()?;
        let kind = decoder.r.u8()?;
        let Some(kind) = NameKind::from_u8(kind) else {
            return error(format!("invalid reference kind {kind}"));
        };
        let target_kind = decoder.r.u8()?;
        let Some(target_kind) = SymbolKind::from_u8(target_kind) else {
            return error(format!("invalid reference target kind {target_kind}"));
        };
        let flags = u32::from(decoder.r.u8()?);
        records.push(ReferenceRecord {
            range,
            name,
            resolved,
            receiver,
            value_start,
            qualified,
            candidates,
            kind,
            target_kind,
            flags,
        });
    }
    let count = decoder.r.len("reference values")?;
    let mut values = Vec::with_capacity(count);
    for _ in 0..count {
        let atom = decoder.atom()?;
        values.push(local_string(atom));
    }
    let Some(references) =
        PackedReferences::from_parts(records, reference_strings, reference_types, values)
    else {
        return error("invalid reference span");
    };
    let count = decoder.r.len("call contracts")?;
    let mut call_contracts = Vec::with_capacity(count);
    for _ in 0..count {
        call_contracts.push(decoder.call_contract()?);
    }
    if decoder.r.position != bytes.len() {
        return error("trailing bytes");
    }
    Ok(PackedDocument {
        path,
        version: document_version,
        workspace_revision,
        namespace,
        symbols: builder.symbols.into_boxed_slice(),
        range_extras: builder.range_extras.into_boxed_slice(),
        type_extras: builder.type_extras.into_boxed_slice(),
        references,
        call_contracts: call_contracts.into_boxed_slice(),
        lazy: match loader {
            Some(loader) if any_lazy => Some(LazyDetails {
                loader,
                pinned: OnceLock::new(),
            }),
            _ => None,
        },
        generation,
    })
}
