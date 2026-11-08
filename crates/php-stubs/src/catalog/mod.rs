//! The compact, generated PHP runtime stub format (port of
//! `internal/php/stubs/catalog`).
//!
//! The wire format is the MessagePack layout written by the Go generator
//! (`cmd/generate_phpstorm_stubs`), so Rust embeds the very same
//! `phpstorm-stubs.msgpack` artifact. Catalog structs are encoded as arrays;
//! the shared semantic records (`Attribute`, `CallContract`, ...) as maps
//! keyed by Go field name; types as their persisted strings.
//!
//! Decoding a `'static` artifact borrows every string from it, so names in
//! materialized symbols never allocate.

mod msgpack;
#[cfg(test)]
mod tests;

use std::borrow::Cow;
use std::collections::{BTreeMap, HashMap, HashSet};

use swlsp_php_project::Version as ProjectVersion;
use swlsp_php_semantic::{
    Attribute, AttributeArgument, AttributeArrayItem, AttributeValue, AttributeValueKind,
    CallContract, CallMapEntry, CallReturnKind, CallTargetKind, CallValue, CallValueKind,
    ExpectedArgumentContract, Flags, Parameter as SemanticParameter, Str, Symbol as SemanticSymbol,
    SymbolId, SymbolKind, TemplateParameter as SemanticTemplate, Visibility, new_symbol_id,
};
use swlsp_php_types::{Type, go_to_lower, parse_persisted};

pub use msgpack::Error;

pub const FORMAT_VERSION: u8 = 6;

/// The deterministic output of the PhpStorm stubs generator. Records that
/// are identical in several PHP versions share one entry and use a bit mask
/// to describe the versions in which they exist.
#[derive(Clone, Debug, Default, PartialEq)]
pub struct Catalog {
    pub format: u8,
    pub repository: Str,
    pub commit: Str,
    pub versions: Vec<Version>,
    pub symbols: Vec<Symbol>,
    pub contracts: Vec<CallContract>,
    pub contract_extensions: Vec<Str>,
    pub bundles: Vec<Bundle>,
    pub extension_symbols: Vec<ExtensionSymbol>,
}

/// One extension's catalog payload as an independently encoded blob, so
/// decoding the catalog header does not build type graphs for every
/// optional extension.
#[derive(Clone, Debug, Default, PartialEq)]
pub struct Bundle {
    pub extension: Str,
    pub data: Cow<'static, [u8]>,
}

#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct ExtensionSymbol {
    pub name: Str,
    pub extension: Str,
}

#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub struct Version {
    pub major: u8,
    pub minor: u8,
}

#[derive(Clone, Debug, Default, PartialEq)]
pub struct Symbol {
    pub version_mask: u16,
    pub extension: Str,
    pub kind: SymbolKind,
    pub name: Str,
    pub fully_qualified: Str,
    pub container: Str,
    pub visibility: Visibility,
    pub write_visibility: Visibility,
    pub has_write_visibility: bool,
    pub flags: Flags,
    pub ty: Type,
    pub native_type: Type,
    pub doc_type: Type,
    pub return_type: Type,
    pub parameters: Vec<Parameter>,
    pub templates: Vec<TemplateParameter>,
    pub extends: Vec<Str>,
    pub implements: Vec<Str>,
    pub traits: Vec<Str>,
    pub extends_types: Vec<Type>,
    pub implements_types: Vec<Type>,
    pub trait_types: Vec<Type>,
    pub throws: Vec<Type>,
    pub attributes: Vec<Attribute>,
    pub doc_summary: Str,
}

#[derive(Clone, Debug, Default, PartialEq)]
pub struct Parameter {
    pub name: Str,
    pub ty: Type,
    pub native_type: Type,
    pub doc_type: Type,
    pub assistant_tags: Vec<Str>,
    pub attributes: Vec<Attribute>,
    pub default_value: Option<AttributeValue>,
    pub flags: Flags,
    pub optional: bool,
}

#[derive(Clone, Debug, Default, PartialEq)]
pub struct TemplateParameter {
    pub name: Str,
    pub bound: Type,
    pub default: Type,
    pub covariant: bool,
    pub contravariant: bool,
}

#[derive(Default)]
struct BundlePayload {
    symbols: Vec<Symbol>,
    contracts: Vec<CallContract>,
}

impl Catalog {
    /// Decodes a catalog, copying its strings.
    pub fn decode(data: &[u8]) -> Result<Catalog, Error> {
        let mut decoder = Decoder::new(data, |value: &str| Str::from(value), owned_blob);
        decoder.catalog().and_then(validate)
    }

    /// Decodes an embedded catalog; strings and bundle blobs borrow `data`.
    pub fn decode_static(data: &'static [u8]) -> Result<Catalog, Error> {
        let mut decoder = Decoder::new(data, Str::from_static, Cow::Borrowed);
        decoder.catalog().and_then(validate)
    }

    pub fn encode(&self) -> Vec<u8> {
        let mut writer = msgpack::Writer::default();
        encode_catalog(&mut writer, self);
        writer.data
    }

    /// Converts generator output into lazy extension payloads. It is
    /// deterministic and clears the expanded collections.
    pub fn pack_bundles(&mut self) {
        if !self.bundles.is_empty() {
            return;
        }
        let mut grouped: BTreeMap<String, BundlePayload> = BTreeMap::new();
        let mut seen_names = HashSet::new();
        for symbol in std::mem::take(&mut self.symbols) {
            let extension = normalize_extension(&symbol.extension);
            if symbol.container.is_empty()
                && !symbol.fully_qualified.is_empty()
                && !extension.is_empty()
                && seen_names.insert((go_to_lower(&symbol.fully_qualified), extension.clone()))
            {
                self.extension_symbols.push(ExtensionSymbol {
                    name: symbol.fully_qualified.clone(),
                    extension: extension.as_str().into(),
                });
            }
            grouped.entry(extension).or_default().symbols.push(symbol);
        }
        let extensions = std::mem::take(&mut self.contract_extensions);
        for (index, contract) in std::mem::take(&mut self.contracts).into_iter().enumerate() {
            let extension = extensions
                .get(index)
                .map_or_else(String::new, |extension| normalize_extension(extension));
            grouped
                .entry(extension)
                .or_default()
                .contracts
                .push(contract);
        }
        for (extension, payload) in grouped {
            let mut writer = msgpack::Writer::default();
            writer.array(2);
            write_list(&mut writer, &payload.symbols, encode_symbol);
            write_list(&mut writer, &payload.contracts, encode_contract);
            self.bundles.push(Bundle {
                extension: extension.as_str().into(),
                data: Cow::Owned(writer.data),
            });
        }
        self.extension_symbols.sort_by(|left, right| {
            (left.name.as_str(), left.extension.as_str())
                .cmp(&(right.name.as_str(), right.extension.as_str()))
        });
    }

    /// The bit for the nearest generated PHP version at or below `version`.
    /// Requests older than the catalog use its oldest entry; newer PHP
    /// releases use the newest known entry.
    pub fn version_mask(&self, version: ProjectVersion) -> u16 {
        if self.versions.is_empty() {
            return 0;
        }
        let mut selected = 0;
        for (index, candidate) in self.versions.iter().enumerate() {
            let major = i64::from(candidate.major);
            if major > version.major
                || (major == version.major && i64::from(candidate.minor) > version.minor)
            {
                break;
            }
            selected = index;
        }
        1u16 << selected
    }

    /// Independent semantic declarations for one PHP version, including
    /// every extension.
    pub fn materialize(&self, version: ProjectVersion, path: &str) -> Vec<SemanticSymbol> {
        self.materialize_for_extensions(version, path, None)
    }

    /// Declarations of the selected extension bundles only. `None` keeps the
    /// complete catalog for tests and tools that request every extension.
    pub fn materialize_for_extensions(
        &self,
        version: ProjectVersion,
        path: &str,
        extensions: Option<&[String]>,
    ) -> Vec<SemanticSymbol> {
        let mask = self.version_mask(version);
        if mask == 0 {
            return Vec::new();
        }
        let selected = extensions.map(extension_selection);
        let enabled = |extension: &str| {
            selected
                .as_ref()
                .is_none_or(|selected| selected.contains(&normalize_extension(extension)))
        };
        let mut sources: Vec<Symbol> = self.symbols.clone();
        for bundle in &self.bundles {
            if !enabled(&bundle.extension) {
                continue;
            }
            let payload = decode_bundle(bundle).unwrap_or_else(|error| {
                panic!("decode PHP stub bundle {}: {error}", bundle.extension)
            });
            if sources.is_empty() {
                sources = payload.symbols;
            } else {
                sources.extend(payload.symbols);
            }
        }
        materialize_symbols(sources, mask, path, |extension| {
            extension.is_empty() || enabled(extension)
        })
    }

    /// Every dynamic call contract.
    pub fn materialize_contracts(&self) -> Vec<CallContract> {
        if !self.bundles.is_empty() {
            return self.materialize_contracts_for_extensions(None);
        }
        self.contracts.clone()
    }

    pub fn materialize_contracts_for_extensions(
        &self,
        extensions: Option<&[String]>,
    ) -> Vec<CallContract> {
        let selected = extensions.map(extension_selection);
        let enabled = |extension: &str| {
            selected
                .as_ref()
                .is_none_or(|selected| selected.contains(&normalize_extension(extension)))
        };
        if !self.bundles.is_empty() {
            let mut result = Vec::new();
            for bundle in &self.bundles {
                if !enabled(&bundle.extension) {
                    continue;
                }
                let payload = decode_bundle(bundle).unwrap_or_else(|error| {
                    panic!("decode PHP stub bundle {}: {error}", bundle.extension)
                });
                result.extend(payload.contracts);
            }
            return result;
        }
        if selected.is_none() || self.contract_extensions.len() != self.contracts.len() {
            return self.contracts.clone();
        }
        self.contracts
            .iter()
            .zip(&self.contract_extensions)
            .filter(|(_, extension)| extension.is_empty() || enabled(extension))
            .map(|(contract, _)| contract.clone())
            .collect()
    }
}

fn validate(catalog: Catalog) -> Result<Catalog, Error> {
    if catalog.format != FORMAT_VERSION {
        return Err(Error(format!(
            "decode PHP stub catalog: unsupported format {}",
            catalog.format
        )));
    }
    if catalog.versions.is_empty() || catalog.versions.len() > 16 {
        return Err(Error(format!(
            "decode PHP stub catalog: invalid version count {}",
            catalog.versions.len()
        )));
    }
    Ok(catalog)
}

fn materialize_symbols(
    sources: Vec<Symbol>,
    mask: u16,
    path: &str,
    enabled: impl Fn(&str) -> bool,
) -> Vec<SemanticSymbol> {
    let path_str = Str::from(path);
    let mut result = Vec::with_capacity(sources.len());
    let mut container_names = Vec::with_capacity(sources.len());
    for source in sources {
        if source.version_mask & mask == 0 || !enabled(&source.extension) {
            continue;
        }
        let id = new_symbol_id(source.kind, &source.fully_qualified, path, 0);
        let parameters = source
            .parameters
            .into_iter()
            .map(|parameter| SemanticParameter {
                id: new_symbol_id(
                    SymbolKind::Parameter,
                    &format!("{}:{}", source.fully_qualified, parameter.name),
                    path,
                    0,
                ),
                name: parameter.name,
                ty: parameter.ty,
                native_type: parameter.native_type,
                doc_type: parameter.doc_type,
                assistant_tags: parameter.assistant_tags,
                attributes: parameter.attributes,
                default_value: parameter.default_value.map(Box::new),
                flags: parameter.flags,
                optional: parameter.optional,
                ..SemanticParameter::default()
            })
            .collect();
        let templates = source
            .templates
            .into_iter()
            .map(|template| SemanticTemplate {
                name: template.name,
                bound: template.bound,
                default: template.default,
                covariant: template.covariant,
                contravariant: template.contravariant,
            })
            .collect();
        let mut symbol = SemanticSymbol {
            id,
            kind: source.kind,
            name: source.name,
            fully_qualified: source.fully_qualified,
            path: path_str.clone(),
            visibility: source.visibility,
            write_visibility: source.write_visibility,
            has_write_visibility: source.has_write_visibility,
            flags: source.flags | Flags::INTERNAL,
            ty: source.ty,
            native_type: source.native_type,
            doc_type: source.doc_type,
            return_type: source.return_type,
            parameters,
            ..SemanticSymbol::default()
        };
        symbol.set_signature_extras(templates, source.throws, Vec::new(), Vec::new(), Vec::new());
        symbol.set_hierarchy(
            source.extends,
            source.implements,
            source.traits,
            source.extends_types,
            source.implements_types,
            source.trait_types,
            Vec::new(),
        );
        symbol.set_metadata(source.attributes, Vec::new(), source.doc_summary);
        result.push(symbol);
        container_names.push(source.container);
    }
    let mut owners: HashMap<String, SymbolId> = HashMap::new();
    for symbol in &result {
        if symbol.is_class_like() {
            owners.insert(go_to_lower(&symbol.fully_qualified), symbol.id.clone());
        }
    }
    for (symbol, container) in result.iter_mut().zip(&container_names) {
        if container.is_empty() {
            continue;
        }
        if let Some(owner) = owners.get(&go_to_lower(container)) {
            symbol.container = owner.clone();
        }
    }
    result
}

fn decode_bundle(bundle: &Bundle) -> Result<BundlePayload, Error> {
    match &bundle.data {
        Cow::Borrowed(data) => Decoder::new(data, Str::from_static, Cow::Borrowed).bundle_payload(),
        Cow::Owned(data) => {
            Decoder::new(data, |value: &str| Str::from(value), owned_blob).bundle_payload()
        }
    }
}

fn extension_selection(extensions: &[String]) -> HashSet<String> {
    extensions
        .iter()
        .map(|extension| normalize_extension(extension))
        .filter(|extension| !extension.is_empty())
        .collect()
}

/// Go `normalizeExtension`: lowercase, without `ext-`, `-` as `_`.
pub fn normalize_extension(extension: &str) -> String {
    let extension = go_to_lower(extension.trim());
    extension
        .strip_prefix("ext-")
        .unwrap_or(&extension)
        .replace('-', "_")
}

// -- decoding ------------------------------------------------------------------

fn owned_blob(data: &[u8]) -> Cow<'static, [u8]> {
    Cow::Owned(data.to_vec())
}

/// Decodes catalog records from `data`. `make` turns borrowed strings into
/// [`Str`]s and `blob` bundle payloads into owned or `'static` data, so a
/// `'static` input is decoded without copying.
struct Decoder<'a, F: Fn(&'a str) -> Str> {
    reader: msgpack::Reader<'a>,
    make: F,
    blob: fn(&'a [u8]) -> Cow<'static, [u8]>,
    types: HashMap<&'a str, Type>,
}

impl<'a, F: Fn(&'a str) -> Str> Decoder<'a, F> {
    fn new(data: &'a [u8], make: F, blob: fn(&'a [u8]) -> Cow<'static, [u8]>) -> Self {
        Decoder {
            reader: msgpack::Reader::new(data),
            make,
            blob,
            types: HashMap::new(),
        }
    }

    fn str(&mut self) -> Result<Str, Error> {
        let value = self.reader.str()?;
        Ok(if value.is_empty() {
            Str::default()
        } else {
            (self.make)(value)
        })
    }

    fn ty(&mut self) -> Result<Type, Error> {
        let text = self.reader.str()?;
        if let Some(value) = self.types.get(text) {
            return Ok(value.clone());
        }
        let value = parse_persisted(text)
            .map_err(|error| Error(format!("parse persisted PHP type {text:?}: {error}")))?;
        self.types.insert(text, value.clone());
        Ok(value)
    }

    fn list<T>(
        &mut self,
        mut item: impl FnMut(&mut Self) -> Result<T, Error>,
    ) -> Result<Vec<T>, Error> {
        let count = self.reader.array()?;
        let mut result = Vec::with_capacity(count);
        for _ in 0..count {
            result.push(item(self)?);
        }
        Ok(result)
    }

    fn strs(&mut self) -> Result<Vec<Str>, Error> {
        self.list(Self::str)
    }

    fn types(&mut self) -> Result<Vec<Type>, Error> {
        self.list(Self::ty)
    }

    /// Reads an array-encoded struct; missing trailing fields keep their
    /// defaults and unknown trailing fields are skipped.
    fn tuple(
        &mut self,
        mut field: impl FnMut(&mut Self, usize) -> Result<(), Error>,
    ) -> Result<(), Error> {
        let count = self.reader.array()?;
        for index in 0..count {
            field(self, index)?;
        }
        Ok(())
    }

    /// Reads a map-encoded struct keyed by Go field name; unknown fields are
    /// skipped.
    fn record(
        &mut self,
        mut field: impl FnMut(&mut Self, &'a str) -> Result<bool, Error>,
    ) -> Result<(), Error> {
        let count = self.reader.map()?;
        for _ in 0..count {
            let key = self.reader.str()?;
            if !field(self, key)? {
                self.reader.skip()?;
            }
        }
        Ok(())
    }

    fn catalog(&mut self) -> Result<Catalog, Error> {
        let mut catalog = Catalog::default();
        self.tuple(|decoder, index| {
            match index {
                0 => catalog.format = decoder.reader.uint()?,
                1 => catalog.repository = decoder.str()?,
                2 => catalog.commit = decoder.str()?,
                3 => {
                    catalog.versions = decoder.list(|decoder| {
                        let mut version = Version::default();
                        decoder.tuple(|decoder, index| {
                            match index {
                                0 => version.major = decoder.reader.uint()?,
                                1 => version.minor = decoder.reader.uint()?,
                                _ => decoder.reader.skip()?,
                            }
                            Ok(())
                        })?;
                        Ok(version)
                    })?;
                }
                4 => catalog.symbols = decoder.list(Self::symbol)?,
                5 => catalog.contracts = decoder.list(Self::contract)?,
                6 => catalog.contract_extensions = decoder.strs()?,
                7 => {
                    catalog.bundles = decoder.list(|decoder| {
                        let mut bundle = Bundle::default();
                        decoder.tuple(|decoder, index| {
                            match index {
                                0 => bundle.extension = decoder.str()?,
                                1 => {
                                    let data = decoder.reader.bin()?;
                                    bundle.data = (decoder.blob)(data);
                                }
                                _ => decoder.reader.skip()?,
                            }
                            Ok(())
                        })?;
                        Ok(bundle)
                    })?;
                }
                8 => {
                    catalog.extension_symbols = decoder.list(|decoder| {
                        let mut symbol = ExtensionSymbol::default();
                        decoder.tuple(|decoder, index| {
                            match index {
                                0 => symbol.name = decoder.str()?,
                                1 => symbol.extension = decoder.str()?,
                                _ => decoder.reader.skip()?,
                            }
                            Ok(())
                        })?;
                        Ok(symbol)
                    })?;
                }
                _ => decoder.reader.skip()?,
            }
            Ok(())
        })?;
        Ok(catalog)
    }

    fn bundle_payload(&mut self) -> Result<BundlePayload, Error> {
        let mut payload = BundlePayload::default();
        self.tuple(|decoder, index| {
            match index {
                0 => payload.symbols = decoder.list(Self::symbol)?,
                1 => payload.contracts = decoder.list(Self::contract)?,
                _ => decoder.reader.skip()?,
            }
            Ok(())
        })?;
        Ok(payload)
    }

    fn symbol(&mut self) -> Result<Symbol, Error> {
        let mut symbol = Symbol::default();
        self.tuple(|decoder, index| {
            match index {
                0 => symbol.version_mask = decoder.reader.uint()?,
                1 => symbol.extension = decoder.str()?,
                2 => symbol.kind = symbol_kind(decoder.reader.uint()?)?,
                3 => symbol.name = decoder.str()?,
                4 => symbol.fully_qualified = decoder.str()?,
                5 => symbol.container = decoder.str()?,
                6 => symbol.visibility = visibility(decoder.reader.uint()?)?,
                7 => symbol.write_visibility = visibility(decoder.reader.uint()?)?,
                8 => symbol.has_write_visibility = decoder.reader.bool()?,
                9 => symbol.flags = Flags(decoder.reader.uint()?),
                10 => symbol.ty = decoder.ty()?,
                11 => symbol.native_type = decoder.ty()?,
                12 => symbol.doc_type = decoder.ty()?,
                13 => symbol.return_type = decoder.ty()?,
                14 => symbol.parameters = decoder.list(Self::parameter)?,
                15 => symbol.templates = decoder.list(Self::template)?,
                16 => symbol.extends = decoder.strs()?,
                17 => symbol.implements = decoder.strs()?,
                18 => symbol.traits = decoder.strs()?,
                19 => symbol.extends_types = decoder.types()?,
                20 => symbol.implements_types = decoder.types()?,
                21 => symbol.trait_types = decoder.types()?,
                22 => symbol.throws = decoder.types()?,
                23 => symbol.attributes = decoder.list(Self::attribute)?,
                24 => symbol.doc_summary = decoder.str()?,
                _ => decoder.reader.skip()?,
            }
            Ok(())
        })?;
        Ok(symbol)
    }

    fn parameter(&mut self) -> Result<Parameter, Error> {
        let mut parameter = Parameter::default();
        self.tuple(|decoder, index| {
            match index {
                0 => parameter.name = decoder.str()?,
                1 => parameter.ty = decoder.ty()?,
                2 => parameter.native_type = decoder.ty()?,
                3 => parameter.doc_type = decoder.ty()?,
                4 => parameter.assistant_tags = decoder.strs()?,
                5 => parameter.attributes = decoder.list(Self::attribute)?,
                6 => {
                    if !decoder.reader.nil()? {
                        parameter.default_value = Some(decoder.attribute_value()?);
                    }
                }
                7 => parameter.flags = Flags(decoder.reader.uint()?),
                8 => parameter.optional = decoder.reader.bool()?,
                _ => decoder.reader.skip()?,
            }
            Ok(())
        })?;
        Ok(parameter)
    }

    fn template(&mut self) -> Result<TemplateParameter, Error> {
        let mut template = TemplateParameter::default();
        self.tuple(|decoder, index| {
            match index {
                0 => template.name = decoder.str()?,
                1 => template.bound = decoder.ty()?,
                2 => template.default = decoder.ty()?,
                3 => template.covariant = decoder.reader.bool()?,
                4 => template.contravariant = decoder.reader.bool()?,
                _ => decoder.reader.skip()?,
            }
            Ok(())
        })?;
        Ok(template)
    }

    fn attribute(&mut self) -> Result<Attribute, Error> {
        let mut attribute = Attribute::default();
        self.record(|decoder, key| {
            match key {
                "Name" => attribute.name = decoder.str()?,
                "Arguments" => {
                    attribute.arguments = decoder.list(|decoder| {
                        let mut argument = AttributeArgument::default();
                        decoder.record(|decoder, key| {
                            match key {
                                "Name" => argument.name = decoder.str()?,
                                "Value" => argument.value = decoder.attribute_value()?,
                                _ => return Ok(false),
                            }
                            Ok(true)
                        })?;
                        Ok(argument)
                    })?;
                }
                _ => return Ok(false),
            }
            Ok(true)
        })?;
        Ok(attribute)
    }

    fn attribute_value(&mut self) -> Result<AttributeValue, Error> {
        let mut value = AttributeValue::default();
        self.record(|decoder, key| {
            match key {
                "Kind" => {
                    let kind: u8 = decoder.reader.uint()?;
                    value.kind = AttributeValueKind::from_u8(kind)
                        .ok_or_else(|| Error(format!("invalid attribute value kind {kind}")))?;
                }
                "Value" => value.value = decoder.str()?,
                "Expression" => value.expression = decoder.str()?,
                "Items" => {
                    value.items = decoder.list(|decoder| {
                        let mut item = AttributeArrayItem::default();
                        decoder.record(|decoder, key| {
                            match key {
                                "Key" => item.key = decoder.attribute_value()?,
                                "HasKey" => item.has_key = decoder.reader.bool()?,
                                "Value" => item.value = decoder.attribute_value()?,
                                _ => return Ok(false),
                            }
                            Ok(true)
                        })?;
                        Ok(item)
                    })?;
                }
                _ => return Ok(false),
            }
            Ok(true)
        })?;
        Ok(value)
    }

    fn contract(&mut self) -> Result<CallContract, Error> {
        let mut contract = CallContract::default();
        self.record(|decoder, key| {
            match key {
                "Target" => {
                    let target = &mut contract.target;
                    decoder.record(|decoder, key| {
                        match key {
                            "Kind" => {
                                target.kind = match decoder.reader.uint::<u8>()? {
                                    0 => CallTargetKind::Function,
                                    1 => CallTargetKind::Method,
                                    kind => {
                                        return Err(Error(format!(
                                            "invalid call target kind {kind}"
                                        )));
                                    }
                                }
                            }
                            "Name" => target.name = decoder.str()?,
                            "Class" => target.class = decoder.str()?,
                            _ => return Ok(false),
                        }
                        Ok(true)
                    })?;
                }
                "Return" => {
                    let result = &mut contract.return_contract;
                    decoder.record(|decoder, key| {
                        match key {
                            "Kind" => {
                                result.kind = match decoder.reader.uint::<u8>()? {
                                    0 => CallReturnKind::Unknown,
                                    1 => CallReturnKind::ArgumentType,
                                    2 => CallReturnKind::ArgumentElementType,
                                    3 => CallReturnKind::ArgumentMap,
                                    kind => {
                                        return Err(Error(format!(
                                            "invalid call return kind {kind}"
                                        )));
                                    }
                                }
                            }
                            "Argument" => result.argument = decoder.reader.uint()?,
                            "Map" => {
                                result.map = decoder.list(|decoder| {
                                    let mut entry = CallMapEntry::default();
                                    decoder.record(|decoder, key| {
                                        match key {
                                            "Key" => entry.key = decoder.call_value()?,
                                            "Result" => entry.result = decoder.call_value()?,
                                            _ => return Ok(false),
                                        }
                                        Ok(true)
                                    })?;
                                    Ok(entry)
                                })?;
                            }
                            _ => return Ok(false),
                        }
                        Ok(true)
                    })?;
                }
                "ExpectedArguments" => {
                    contract.expected_arguments = decoder.expected_arguments()?
                }
                "ExpectedReturnValues" => {
                    contract.expected_return_values = decoder.list(Self::call_value)?;
                }
                "ExitPoint" => contract.exit_point = decoder.reader.bool()?,
                "ExitArguments" => contract.exit_arguments = decoder.expected_arguments()?,
                _ => return Ok(false),
            }
            Ok(true)
        })?;
        Ok(contract)
    }

    fn expected_arguments(&mut self) -> Result<Vec<ExpectedArgumentContract>, Error> {
        self.list(|decoder| {
            let mut expected = ExpectedArgumentContract::default();
            decoder.record(|decoder, key| {
                match key {
                    "Argument" => expected.argument = decoder.reader.uint()?,
                    "Values" => expected.values = decoder.list(Self::call_value)?,
                    _ => return Ok(false),
                }
                Ok(true)
            })?;
            Ok(expected)
        })
    }

    fn call_value(&mut self) -> Result<CallValue, Error> {
        let mut value = CallValue::default();
        self.record(|decoder, key| {
            match key {
                "Kind" => {
                    value.kind = match decoder.reader.uint::<u8>()? {
                        0 => CallValueKind::Expression,
                        1 => CallValueKind::String,
                        2 => CallValueKind::Number,
                        3 => CallValueKind::Constant,
                        4 => CallValueKind::ClassConstant,
                        kind => return Err(Error(format!("invalid call value kind {kind}"))),
                    }
                }
                "Value" => value.value = decoder.str()?,
                "Expression" => value.expression = decoder.str()?,
                _ => return Ok(false),
            }
            Ok(true)
        })?;
        Ok(value)
    }
}

fn symbol_kind(value: u8) -> Result<SymbolKind, Error> {
    SymbolKind::from_u8(value).ok_or_else(|| Error(format!("invalid symbol kind {value}")))
}

fn visibility(value: u8) -> Result<Visibility, Error> {
    Visibility::from_u8(value).ok_or_else(|| Error(format!("invalid visibility {value}")))
}

// -- encoding ------------------------------------------------------------------

fn write_list<T>(writer: &mut msgpack::Writer, items: &[T], encode: fn(&mut msgpack::Writer, &T)) {
    if items.is_empty() {
        writer.nil();
        return;
    }
    writer.array(items.len());
    for item in items {
        encode(writer, item);
    }
}

fn write_strs(writer: &mut msgpack::Writer, items: &[Str]) {
    write_list(writer, items, |writer, item| writer.str(item));
}

fn write_type(writer: &mut msgpack::Writer, value: &Type) {
    writer.str(&value.persisted_string());
}

fn write_types(writer: &mut msgpack::Writer, items: &[Type]) {
    write_list(writer, items, write_type);
}

fn encode_catalog(writer: &mut msgpack::Writer, catalog: &Catalog) {
    writer.array(9);
    writer.uint(u64::from(catalog.format));
    writer.str(&catalog.repository);
    writer.str(&catalog.commit);
    write_list(writer, &catalog.versions, |writer, version| {
        writer.array(2);
        writer.uint(u64::from(version.major));
        writer.uint(u64::from(version.minor));
    });
    write_list(writer, &catalog.symbols, encode_symbol);
    write_list(writer, &catalog.contracts, encode_contract);
    write_strs(writer, &catalog.contract_extensions);
    write_list(writer, &catalog.bundles, |writer, bundle| {
        writer.array(2);
        writer.str(&bundle.extension);
        writer.bin(&bundle.data);
    });
    write_list(writer, &catalog.extension_symbols, |writer, symbol| {
        writer.array(2);
        writer.str(&symbol.name);
        writer.str(&symbol.extension);
    });
}

fn encode_symbol(writer: &mut msgpack::Writer, symbol: &Symbol) {
    writer.array(25);
    writer.uint(u64::from(symbol.version_mask));
    writer.str(&symbol.extension);
    writer.uint(symbol.kind as u64);
    writer.str(&symbol.name);
    writer.str(&symbol.fully_qualified);
    writer.str(&symbol.container);
    writer.uint(symbol.visibility as u64);
    writer.uint(symbol.write_visibility as u64);
    writer.bool(symbol.has_write_visibility);
    writer.uint(u64::from(symbol.flags.0));
    write_type(writer, &symbol.ty);
    write_type(writer, &symbol.native_type);
    write_type(writer, &symbol.doc_type);
    write_type(writer, &symbol.return_type);
    write_list(writer, &symbol.parameters, encode_parameter);
    write_list(writer, &symbol.templates, |writer, template| {
        writer.array(5);
        writer.str(&template.name);
        write_type(writer, &template.bound);
        write_type(writer, &template.default);
        writer.bool(template.covariant);
        writer.bool(template.contravariant);
    });
    write_strs(writer, &symbol.extends);
    write_strs(writer, &symbol.implements);
    write_strs(writer, &symbol.traits);
    write_types(writer, &symbol.extends_types);
    write_types(writer, &symbol.implements_types);
    write_types(writer, &symbol.trait_types);
    write_types(writer, &symbol.throws);
    write_list(writer, &symbol.attributes, encode_attribute);
    writer.str(&symbol.doc_summary);
}

fn encode_parameter(writer: &mut msgpack::Writer, parameter: &Parameter) {
    writer.array(9);
    writer.str(&parameter.name);
    write_type(writer, &parameter.ty);
    write_type(writer, &parameter.native_type);
    write_type(writer, &parameter.doc_type);
    write_strs(writer, &parameter.assistant_tags);
    write_list(writer, &parameter.attributes, encode_attribute);
    match &parameter.default_value {
        Some(value) => encode_attribute_value(writer, value),
        None => writer.nil(),
    }
    writer.uint(u64::from(parameter.flags.0));
    writer.bool(parameter.optional);
}

fn encode_attribute(writer: &mut msgpack::Writer, attribute: &Attribute) {
    writer.map(2);
    writer.str("Name");
    writer.str(&attribute.name);
    writer.str("Arguments");
    write_list(writer, &attribute.arguments, |writer, argument| {
        writer.map(2);
        writer.str("Name");
        writer.str(&argument.name);
        writer.str("Value");
        encode_attribute_value(writer, &argument.value);
    });
}

fn encode_attribute_value(writer: &mut msgpack::Writer, value: &AttributeValue) {
    writer.map(4);
    writer.str("Kind");
    writer.uint(value.kind as u64);
    writer.str("Value");
    writer.str(&value.value);
    writer.str("Expression");
    writer.str(&value.expression);
    writer.str("Items");
    write_list(writer, &value.items, |writer, item| {
        writer.map(3);
        writer.str("Key");
        encode_attribute_value(writer, &item.key);
        writer.str("HasKey");
        writer.bool(item.has_key);
        writer.str("Value");
        encode_attribute_value(writer, &item.value);
    });
}

fn encode_contract(writer: &mut msgpack::Writer, contract: &CallContract) {
    writer.map(6);
    writer.str("Target");
    writer.map(3);
    writer.str("Kind");
    writer.uint(contract.target.kind as u64);
    writer.str("Name");
    writer.str(&contract.target.name);
    writer.str("Class");
    writer.str(&contract.target.class);
    writer.str("Return");
    writer.map(3);
    writer.str("Kind");
    writer.uint(contract.return_contract.kind as u64);
    writer.str("Argument");
    writer.uint(u64::from(contract.return_contract.argument));
    writer.str("Map");
    write_list(writer, &contract.return_contract.map, |writer, entry| {
        writer.map(2);
        writer.str("Key");
        encode_call_value(writer, &entry.key);
        writer.str("Result");
        encode_call_value(writer, &entry.result);
    });
    writer.str("ExpectedArguments");
    write_list(
        writer,
        &contract.expected_arguments,
        encode_expected_argument,
    );
    writer.str("ExpectedReturnValues");
    write_list(writer, &contract.expected_return_values, encode_call_value);
    writer.str("ExitPoint");
    writer.bool(contract.exit_point);
    writer.str("ExitArguments");
    write_list(writer, &contract.exit_arguments, encode_expected_argument);
}

fn encode_expected_argument(writer: &mut msgpack::Writer, expected: &ExpectedArgumentContract) {
    writer.map(2);
    writer.str("Argument");
    writer.uint(u64::from(expected.argument));
    writer.str("Values");
    write_list(writer, &expected.values, encode_call_value);
}

fn encode_call_value(writer: &mut msgpack::Writer, value: &CallValue) {
    writer.map(3);
    writer.str("Kind");
    writer.uint(value.kind as u64);
    writer.str("Value");
    writer.str(&value.value);
    writer.str("Expression");
    writer.str(&value.expression);
}
