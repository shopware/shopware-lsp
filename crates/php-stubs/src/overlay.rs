//! Hand-written refinements layered over the generated catalog. Each declaration replaces a generated one with the same
//! kind and name.

use swlsp_php_project::Version;
use swlsp_php_semantic::{SymbolId, SymbolKind, Visibility};
use swlsp_php_types::{ShapeField, Type};

use crate::{StubBuilder, by_reference, parameter, template, variadic};

macro_rules! named {
    ($name:expr $(, $argument:expr)* $(,)?) => {
        Type::named($name, vec![$($argument),*])
    };
}

macro_rules! union {
    ($($member:expr),* $(,)?) => {
        Type::union([$($member),*])
    };
}

macro_rules! method {
    ($builder:expr, $container:expr, $name:expr, $result:expr $(, $parameter:expr)* $(,)?) => {
        $builder.method(&$container, $name, $result, vec![$($parameter),*])
    };
}

macro_rules! static_method {
    ($builder:expr, $container:expr, $name:expr, $result:expr $(, $parameter:expr)* $(,)?) => {
        $builder.static_method(&$container, $name, $result, vec![$($parameter),*])
    };
}

macro_rules! method_with_templates {
    ($builder:expr, $container:expr, $name:expr, $result:expr, $templates:expr $(, $parameter:expr)* $(,)?) => {
        $builder.method_with_templates(&$container, $name, $result, $templates, vec![$($parameter),*])
    };
}

macro_rules! static_method_with_templates {
    ($builder:expr, $container:expr, $name:expr, $result:expr, $templates:expr $(, $parameter:expr)* $(,)?) => {
        $builder.static_method_with_templates(&$container, $name, $result, $templates, vec![$($parameter),*])
    };
}

macro_rules! function {
    ($builder:expr, $name:expr, $result:expr $(, $parameter:expr)* $(,)?) => {
        $builder.function($name, $result, vec![$($parameter),*])
    };
}

macro_rules! set_templates {
    ($builder:expr, $container:expr $(, $template:expr)* $(,)?) => {
        $builder.set_templates(&$container, vec![$($template),*])
    };
}

pub(crate) fn apply(builder: &mut StubBuilder, version: Version) {
    let trace_frame = Type::array_shape(
        vec![
            ShapeField::optional("args", Type::list(Type::mixed())),
            ShapeField::optional("class", Type::class_string(Type::object())),
            ShapeField::optional("file", Type::string()),
            ShapeField::new("function", Type::string()),
            ShapeField::optional("line", Type::int()),
            ShapeField::optional("object", Type::object()),
            ShapeField::optional("type", Type::string()),
        ],
        true,
    );

    let add_throwable_methods = |builder: &mut StubBuilder, container: &SymbolId| {
        method!(builder, *container, "getMessage", Type::string());
        method!(builder, *container, "getCode", Type::int());
        method!(builder, *container, "getFile", Type::string());
        method!(builder, *container, "getLine", Type::int());
        method!(
            builder,
            *container,
            "getTrace",
            Type::list(trace_frame.clone())
        );
        method!(builder, *container, "getTraceAsString", Type::string());
        method!(
            builder,
            *container,
            "getPrevious",
            Type::nullable(named!("Throwable"))
        );
        method!(builder, *container, "__toString", Type::string());
    };
    let throwable = builder.class("Throwable", SymbolKind::Interface);
    add_throwable_methods(builder, &throwable);
    let exception = builder.class("Exception", SymbolKind::Class);
    builder.add_implements(&exception, &["Throwable"]);
    add_throwable_methods(builder, &exception);
    let error_class = builder.class("Error", SymbolKind::Class);
    builder.add_implements(&error_class, &["Throwable"]);
    add_throwable_methods(builder, &error_class);
    for container in [&exception, &error_class] {
        builder.property_visibility(container, "message", Type::string(), Visibility::Protected);
        builder.property_visibility(container, "code", Type::int(), Visibility::Protected);
        builder.property_visibility(container, "file", Type::string(), Visibility::Protected);
        builder.property_visibility(container, "line", Type::int(), Visibility::Protected);
    }
    builder.class_extends("RuntimeException", SymbolKind::Class, "Exception");
    builder.class_extends("LogicException", SymbolKind::Class, "Exception");
    builder.class_extends(
        "InvalidArgumentException",
        SymbolKind::Class,
        "LogicException",
    );
    builder.class_extends(
        "BadFunctionCallException",
        SymbolKind::Class,
        "LogicException",
    );
    builder.class_extends(
        "BadMethodCallException",
        SymbolKind::Class,
        "BadFunctionCallException",
    );
    builder.class_extends("DomainException", SymbolKind::Class, "LogicException");
    builder.class_extends("LengthException", SymbolKind::Class, "LogicException");
    builder.class_extends("OutOfRangeException", SymbolKind::Class, "LogicException");
    builder.class_extends(
        "OutOfBoundsException",
        SymbolKind::Class,
        "RuntimeException",
    );
    builder.class_extends("OverflowException", SymbolKind::Class, "RuntimeException");
    builder.class_extends("RangeException", SymbolKind::Class, "RuntimeException");
    builder.class_extends("UnderflowException", SymbolKind::Class, "RuntimeException");
    builder.class_extends(
        "UnexpectedValueException",
        SymbolKind::Class,
        "RuntimeException",
    );
    builder.class_extends("ErrorException", SymbolKind::Class, "Exception");
    builder.class_extends("TypeError", SymbolKind::Class, "Error");
    builder.class_extends("ArgumentCountError", SymbolKind::Class, "TypeError");
    builder.class_extends("ValueError", SymbolKind::Class, "Error");
    builder.class_extends("ArithmeticError", SymbolKind::Class, "Error");
    builder.class_extends("DivisionByZeroError", SymbolKind::Class, "ArithmeticError");
    builder.class_extends("AssertionError", SymbolKind::Class, "Error");
    builder.class_extends("ParseError", SymbolKind::Class, "Error");
    builder.class_extends("UnhandledMatchError", SymbolKind::Class, "Error");
    builder.class_extends("JsonException", SymbolKind::Class, "Exception");
    builder.class_extends("ReflectionException", SymbolKind::Class, "Exception");
    method!(
        builder,
        exception,
        "__construct",
        Type::void(),
        parameter("$message", Type::string(), true),
        parameter("$code", Type::int(), true),
        parameter("$previous", Type::nullable(named!("Throwable")), true),
    );
    method!(
        builder,
        error_class,
        "__construct",
        Type::void(),
        parameter("$message", Type::string(), true),
        parameter("$code", Type::int(), true),
        parameter("$previous", Type::nullable(named!("Throwable")), true),
    );
    builder.class("Traversable", SymbolKind::Interface);
    let iterator = builder.class_extends("Iterator", SymbolKind::Interface, "Traversable");
    method!(builder, iterator, "current", Type::mixed());
    method!(builder, iterator, "key", Type::mixed());
    method!(builder, iterator, "next", Type::void());
    method!(builder, iterator, "valid", Type::bool());
    let iterator_aggregate =
        builder.class_extends("IteratorAggregate", SymbolKind::Interface, "Traversable");
    method!(
        builder,
        iterator_aggregate,
        "getIterator",
        named!("Traversable")
    );
    let countable = builder.class("Countable", SymbolKind::Interface);
    method!(builder, countable, "count", Type::int());
    let array_access = builder.class("ArrayAccess", SymbolKind::Interface);
    method!(
        builder,
        array_access,
        "offsetGet",
        Type::mixed(),
        parameter("$offset", Type::mixed(), false)
    );
    method!(
        builder,
        array_access,
        "offsetExists",
        Type::bool(),
        parameter("$offset", Type::mixed(), false)
    );
    let stringable = builder.class("Stringable", SymbolKind::Interface);
    method!(builder, stringable, "__toString", Type::string());
    let json_serializable = builder.class("JsonSerializable", SymbolKind::Interface);
    method!(builder, json_serializable, "jsonSerialize", Type::mixed());
    builder.class("stdClass", SymbolKind::Class);
    let weak_map = builder.class("WeakMap", SymbolKind::Class);
    builder.add_implements(
        &weak_map,
        &["IteratorAggregate", "ArrayAccess", "Countable"],
    );
    let spl_file_info = builder.class("SplFileInfo", SymbolKind::Class);
    method!(
        builder,
        spl_file_info,
        "__construct",
        Type::void(),
        parameter("$filename", Type::string(), false),
    );
    method!(builder, spl_file_info, "getPathname", Type::string());
    method!(builder, spl_file_info, "getPath", Type::string());
    method!(builder, spl_file_info, "getFilename", Type::string());
    method!(
        builder,
        spl_file_info,
        "getBasename",
        Type::string(),
        parameter("$suffix", Type::string(), true),
    );
    method!(builder, spl_file_info, "getExtension", Type::string());
    method!(
        builder,
        spl_file_info,
        "getRealPath",
        union!(Type::string(), Type::false_type())
    );
    method!(builder, spl_file_info, "getSize", Type::int());
    method!(
        builder,
        spl_file_info,
        "getPerms",
        union!(Type::int(), Type::false_type())
    );
    method!(builder, spl_file_info, "isFile", Type::bool());
    method!(builder, spl_file_info, "isDir", Type::bool());
    let spl_file_object = builder.class_extends("SplFileObject", SymbolKind::Class, "SplFileInfo");
    builder.add_implements(&spl_file_object, &["Iterator"]);
    method!(
        builder,
        spl_file_object,
        "__construct",
        Type::void(),
        parameter("$filename", Type::string(), false),
        parameter("$mode", Type::string(), true),
        parameter("$useIncludePath", Type::bool(), true),
        parameter("$context", Type::resource(), true),
    );
    method!(
        builder,
        spl_file_object,
        "seek",
        Type::void(),
        parameter("$line", Type::int(), false),
    );
    let iterator_iterator = builder.class("IteratorIterator", SymbolKind::Class);
    builder.add_implements(&iterator_iterator, &["Iterator"]);
    let callback_filter_iterator = builder.class_extends(
        "CallbackFilterIterator",
        SymbolKind::Class,
        "IteratorIterator",
    );
    method!(
        builder,
        callback_filter_iterator,
        "__construct",
        Type::void(),
        parameter("$iterator", named!("Iterator"), false),
        parameter("$callback", Type::callable(Vec::new(), Type::bool()), false),
    );
    let filesystem_iterator =
        builder.class_extends("FilesystemIterator", SymbolKind::Class, "IteratorIterator");
    builder.class_constant(&filesystem_iterator, "SKIP_DOTS", Type::int());
    method!(
        builder,
        filesystem_iterator,
        "__construct",
        Type::void(),
        parameter("$directory", Type::string(), false),
        parameter("$flags", Type::int(), true),
    );
    builder.class_extends(
        "RecursiveDirectoryIterator",
        SymbolKind::Class,
        "FilesystemIterator",
    );
    let recursive_iterator_iterator = builder.class("RecursiveIteratorIterator", SymbolKind::Class);
    builder.add_implements(&recursive_iterator_iterator, &["Iterator"]);
    method!(builder, spl_file_object, "eof", Type::bool());

    let array_iterator = builder.class("ArrayIterator", SymbolKind::Class);
    set_templates!(
        builder,
        array_iterator,
        template("TKey", Type::array_key(), Type::array_key()),
        template("TValue", Type::mixed(), Type::mixed()),
    );
    builder.add_implements(&array_iterator, &["Iterator", "ArrayAccess", "Countable"]);
    method!(
        builder,
        array_iterator,
        "__construct",
        Type::void(),
        parameter(
            "$array",
            union!(
                Type::array(Type::template("TKey"), Type::template("TValue"),),
                Type::object(),
            ),
            true,
        ),
        parameter("$flags", Type::int(), true),
    );
    method!(
        builder,
        array_iterator,
        "getArrayCopy",
        Type::array(Type::array_key(), Type::mixed())
    );
    method!(
        builder,
        array_iterator,
        "append",
        Type::void(),
        parameter("$value", Type::mixed(), false),
    );

    let unit_enum = builder.class("UnitEnum", SymbolKind::Interface);
    static_method!(builder, unit_enum, "cases", Type::list(Type::STATIC),);
    let backed_enum = builder.class_extends("BackedEnum", SymbolKind::Interface, "UnitEnum");
    static_method!(
        builder,
        backed_enum,
        "from",
        Type::STATIC,
        parameter("$value", union!(Type::int(), Type::string()), false),
    );
    static_method!(
        builder,
        backed_enum,
        "tryFrom",
        Type::nullable(Type::STATIC),
        parameter("$value", union!(Type::int(), Type::string()), false),
    );

    let closure = builder.class("Closure", SymbolKind::Class);
    method!(
        builder,
        closure,
        "call",
        Type::mixed(),
        parameter("$newThis", Type::object(), false),
        variadic("$args", Type::mixed()),
    );
    method!(
        builder,
        closure,
        "bindTo",
        Type::nullable(named!("Closure")),
        parameter("$newThis", Type::nullable(Type::object()), false),
        parameter(
            "$newScope",
            union!(Type::object(), Type::string(), Type::null()),
            true
        ),
    );
    let closure_template = Type::template("TClosure");
    static_method_with_templates!(
        builder,
        closure,
        "bind",
        Type::nullable(closure_template.clone()),
        vec![template(
            "TClosure",
            Type::callable(Vec::new(), Type::mixed()),
            Type::unknown()
        )],
        parameter("$closure", closure_template, false),
        parameter("$newThis", Type::nullable(Type::object()), false),
        parameter(
            "$newScope",
            union!(Type::object(), Type::string(), Type::null()),
            true
        ),
    );
    static_method!(
        builder,
        closure,
        "fromCallable",
        named!("Closure"),
        parameter(
            "$callback",
            Type::callable(Vec::new(), Type::mixed()),
            false
        ),
    );

    let date_time = builder.class("DateTimeInterface", SymbolKind::Interface);
    builder.class_constant(&date_time, "ATOM", Type::string());
    builder.class_constant(&date_time, "RFC3339_EXTENDED", Type::string());
    method!(
        builder,
        date_time,
        "format",
        Type::string(),
        parameter("$format", Type::string(), false)
    );
    method!(builder, date_time, "getTimestamp", Type::int());
    method!(
        builder,
        date_time,
        "diff",
        named!("DateInterval"),
        parameter("$targetObject", named!("DateTimeInterface"), false),
        parameter("$absolute", Type::bool(), true),
    );
    let date_time_zone = builder.class("DateTimeZone", SymbolKind::Class);
    builder.class_constant(&date_time_zone, "ALL_WITH_BC", Type::int());
    method!(
        builder,
        date_time_zone,
        "__construct",
        Type::void(),
        parameter("$timezone", Type::string(), false),
    );
    static_method!(
        builder,
        date_time_zone,
        "listIdentifiers",
        Type::list(Type::string()),
        parameter("$timezoneGroup", Type::int(), true),
        parameter("$countryCode", Type::nullable(Type::string()), true),
    );
    let date_time_mutable =
        builder.class_extends("DateTime", SymbolKind::Class, "DateTimeInterface");
    method!(
        builder,
        date_time_mutable,
        "__construct",
        Type::void(),
        parameter("$datetime", Type::string(), true),
        parameter("$timezone", Type::nullable(named!("DateTimeZone")), true),
    );
    method!(
        builder,
        date_time_mutable,
        "modify",
        union!(Type::STATIC, Type::false_type()),
        parameter("$modifier", Type::string(), false),
    );
    method!(
        builder,
        date_time_mutable,
        "add",
        Type::STATIC,
        parameter("$interval", named!("DateInterval"), false),
    );
    method!(
        builder,
        date_time_mutable,
        "sub",
        Type::STATIC,
        parameter("$interval", named!("DateInterval"), false),
    );
    method!(
        builder,
        date_time_mutable,
        "setTime",
        Type::STATIC,
        parameter("$hour", Type::int(), false),
        parameter("$minute", Type::int(), false),
        parameter("$second", Type::int(), true),
        parameter("$microsecond", Type::int(), true),
    );
    method!(
        builder,
        date_time_mutable,
        "setTimezone",
        Type::STATIC,
        parameter("$timezone", named!("DateTimeZone"), false),
    );
    method!(
        builder,
        date_time_mutable,
        "setTimestamp",
        Type::STATIC,
        parameter("$timestamp", Type::int(), false),
    );
    static_method!(
        builder,
        date_time_mutable,
        "createFromImmutable",
        Type::STATIC,
        parameter("$object", named!("DateTimeImmutable"), false),
    );
    static_method!(
        builder,
        date_time_mutable,
        "createFromFormat",
        union!(Type::STATIC, Type::false_type()),
        parameter("$format", Type::string(), false),
        parameter("$datetime", Type::string(), false),
        parameter("$timezone", Type::nullable(named!("DateTimeZone")), true),
    );
    let date_time_immutable =
        builder.class_extends("DateTimeImmutable", SymbolKind::Class, "DateTimeInterface");
    method!(
        builder,
        date_time_immutable,
        "__construct",
        Type::void(),
        parameter("$datetime", Type::string(), true),
        parameter("$timezone", Type::nullable(named!("DateTimeZone")), true),
    );
    method!(
        builder,
        date_time_immutable,
        "modify",
        union!(Type::STATIC, Type::false_type()),
        parameter("$modifier", Type::string(), false),
    );
    method!(
        builder,
        date_time_immutable,
        "add",
        Type::STATIC,
        parameter("$interval", named!("DateInterval"), false),
    );
    method!(
        builder,
        date_time_immutable,
        "sub",
        Type::STATIC,
        parameter("$interval", named!("DateInterval"), false),
    );
    method!(
        builder,
        date_time_immutable,
        "setTime",
        Type::STATIC,
        parameter("$hour", Type::int(), false),
        parameter("$minute", Type::int(), false),
        parameter("$second", Type::int(), true),
        parameter("$microsecond", Type::int(), true),
    );
    method!(
        builder,
        date_time_immutable,
        "setTimezone",
        Type::STATIC,
        parameter("$timezone", named!("DateTimeZone"), false),
    );
    method!(
        builder,
        date_time_immutable,
        "setTimestamp",
        Type::STATIC,
        parameter("$timestamp", Type::int(), false),
    );
    static_method!(
        builder,
        date_time_immutable,
        "createFromInterface",
        Type::STATIC,
        parameter("$object", named!("DateTimeInterface"), false),
    );
    static_method!(
        builder,
        date_time_immutable,
        "createFromMutable",
        Type::STATIC,
        parameter("$object", named!("DateTime"), false),
    );
    static_method!(
        builder,
        date_time_immutable,
        "createFromFormat",
        union!(Type::STATIC, Type::false_type()),
        parameter("$format", Type::string(), false),
        parameter("$datetime", Type::string(), false),
        parameter("$timezone", Type::nullable(named!("DateTimeZone")), true),
    );
    let date_interval = builder.class("DateInterval", SymbolKind::Class);
    method!(
        builder,
        date_interval,
        "__construct",
        Type::void(),
        parameter("$duration", Type::string(), false),
    );
    static_method!(
        builder,
        date_interval,
        "createFromDateString",
        union!(named!("DateInterval"), Type::false_type()),
        parameter("$datetime", Type::string(), false),
    );
    method!(
        builder,
        date_interval,
        "format",
        Type::string(),
        parameter("$format", Type::string(), false),
    );
    for name in ["y", "m", "d", "h", "i", "s", "f", "invert", "days"] {
        let value = match name {
            "f" => Type::float(),
            "days" => union!(Type::int(), Type::false_type()),
            _ => Type::int(),
        };
        builder.property(&date_interval, name, value);
    }

    let pdo = builder.class("PDO", SymbolKind::Class);
    for name in [
        "ATTR_STRINGIFY_FETCHES",
        "ATTR_TIMEOUT",
        "PARAM_BOOL",
        "PARAM_INT",
        "PARAM_NULL",
        "PARAM_STR",
        "ATTR_PERSISTENT",
    ] {
        builder.class_constant(&pdo, name, Type::int());
    }
    let pdo_mysql = builder.class("Pdo\\Mysql", SymbolKind::Class);
    for name in [
        "ATTR_INIT_COMMAND",
        "ATTR_SSL_CA",
        "ATTR_SSL_CERT",
        "ATTR_SSL_KEY",
        "ATTR_SSL_VERIFY_SERVER_CERT",
        "ATTR_COMPRESS",
    ] {
        builder.class_constant(&pdo_mysql, name, Type::int());
    }

    let dom_node = builder.class("DOMNode", SymbolKind::Class);
    builder.property(&dom_node, "nodeName", Type::string());
    builder.property(&dom_node, "nodeValue", Type::nullable(Type::string()));
    builder.property(&dom_node, "localName", Type::nullable(Type::string()));
    builder.property(&dom_node, "textContent", Type::string());
    builder.property(
        &dom_node,
        "childNodes",
        named!("DOMNodeList", named!("DOMNode")),
    );
    method!(
        builder,
        dom_node,
        "appendChild",
        union!(named!("DOMNode"), Type::false_type()),
        parameter("$node", named!("DOMNode"), false),
    );
    method!(builder, dom_node, "hasChildNodes", Type::bool());
    let dom_element = builder.class_extends("DOMElement", SymbolKind::Class, "DOMNode");
    builder.property(&dom_element, "tagName", Type::string());
    builder.property(&dom_element, "attributes", named!("DOMNamedNodeMap"));
    method!(
        builder,
        dom_element,
        "getAttribute",
        Type::string(),
        parameter("$qualifiedName", Type::string(), false),
    );
    method!(
        builder,
        dom_element,
        "hasAttribute",
        Type::bool(),
        parameter("$qualifiedName", Type::string(), false),
    );
    method!(
        builder,
        dom_element,
        "setAttribute",
        union!(Type::bool(), named!("DOMAttr")),
        parameter("$qualifiedName", Type::string(), false),
        parameter("$value", Type::string(), false),
    );
    let dom_node_list = builder.class("DOMNodeList", SymbolKind::Class);
    set_templates!(
        builder,
        dom_node_list,
        template("TNode", named!("DOMNode"), named!("DOMNode")),
    );
    builder.add_implements(&dom_node_list, &["IteratorAggregate", "Countable"]);
    builder.property(&dom_node_list, "length", Type::int());
    method!(
        builder,
        dom_node_list,
        "item",
        Type::nullable(Type::template("TNode")),
        parameter("$index", Type::int(), false),
    );
    method!(builder, dom_node_list, "count", Type::int());
    method!(builder, dom_node_list, "getIterator", named!("Iterator"));
    method!(
        builder,
        dom_element,
        "getElementsByTagName",
        named!("DOMNodeList", named!("DOMElement")),
        parameter("$qualifiedName", Type::string(), false),
    );
    let dom_attr = builder.class_extends("DOMAttr", SymbolKind::Class, "DOMNode");
    builder.property(&dom_attr, "name", Type::string());
    builder.property(&dom_attr, "value", Type::string());
    let dom_named_node_map = builder.class("DOMNamedNodeMap", SymbolKind::Class);
    builder.add_implements(&dom_named_node_map, &["IteratorAggregate", "Countable"]);
    method!(
        builder,
        dom_named_node_map,
        "getIterator",
        named!("Iterator")
    );
    method!(builder, dom_named_node_map, "count", Type::int());
    builder.class_extends("DOMText", SymbolKind::Class, "DOMNode");
    let dom_document = builder.class_extends("DOMDocument", SymbolKind::Class, "DOMNode");
    builder.property(&dom_document, "formatOutput", Type::bool());
    builder.property(&dom_document, "preserveWhiteSpace", Type::bool());
    builder.property(&dom_document, "encoding", Type::nullable(Type::string()));
    method!(
        builder,
        dom_document,
        "__construct",
        Type::void(),
        parameter("$version", Type::string(), true),
        parameter("$encoding", Type::string(), true),
    );
    method!(
        builder,
        dom_document,
        "loadXML",
        Type::bool(),
        parameter("$source", Type::string(), false),
        parameter("$options", Type::int(), true),
    );
    method!(
        builder,
        dom_document,
        "load",
        Type::bool(),
        parameter("$filename", Type::string(), false),
        parameter("$options", Type::int(), true),
    );
    method!(
        builder,
        dom_document,
        "saveXML",
        union!(Type::string(), Type::false_type()),
        parameter("$node", Type::nullable(named!("DOMNode")), true),
        parameter("$options", Type::int(), true),
    );
    method!(
        builder,
        dom_document,
        "createElement",
        named!("DOMElement"),
        parameter("$localName", Type::string(), false),
        parameter("$value", Type::string(), true),
    );
    method!(
        builder,
        dom_document,
        "createTextNode",
        named!("DOMText"),
        parameter("$data", Type::string(), false),
    );
    method!(
        builder,
        dom_document,
        "getElementsByTagName",
        named!("DOMNodeList", named!("DOMElement")),
        parameter("$qualifiedName", Type::string(), false),
    );
    let dom_x_path = builder.class("DOMXPath", SymbolKind::Class);
    method!(
        builder,
        dom_x_path,
        "__construct",
        Type::void(),
        parameter("$document", named!("DOMDocument"), false),
        parameter("$registerNodeNS", Type::bool(), true),
    );
    method!(
        builder,
        dom_x_path,
        "query",
        union!(named!("DOMNodeList", named!("DOMNode")), Type::false_type(),),
        parameter("$expression", Type::string(), false),
        parameter("$contextNode", Type::nullable(named!("DOMNode")), true),
        parameter("$registerNodeNS", Type::bool(), true),
    );
    let libxml_error = builder.class("LibXMLError", SymbolKind::Class);
    builder.property(&libxml_error, "column", Type::int());
    builder.property(&libxml_error, "line", Type::int());
    builder.property(&libxml_error, "message", Type::string());
    let simple_x_m_l = builder.class("SimpleXMLElement", SymbolKind::Class);
    method!(
        builder,
        simple_x_m_l,
        "__get",
        Type::mixed(),
        parameter("$name", Type::string(), false),
    );
    method!(
        builder,
        simple_x_m_l,
        "attributes",
        Type::nullable(named!("SimpleXMLElement")),
        parameter("$namespaceOrPrefix", Type::nullable(Type::string()), true),
        parameter("$isPrefix", Type::bool(), true),
    );

    builder.class("GdImage", SymbolKind::Class);
    builder.class("CurlHandle", SymbolKind::Class);
    let collator = builder.class("Collator", SymbolKind::Class);
    for name in ["NUMERIC_COLLATION", "ALTERNATE_HANDLING", "ON", "SHIFTED"] {
        builder.class_constant(&collator, name, Type::int());
    }
    method!(
        builder,
        collator,
        "__construct",
        Type::void(),
        parameter("$locale", Type::string(), false),
    );
    method!(
        builder,
        collator,
        "setAttribute",
        Type::bool(),
        parameter("$attribute", Type::int(), false),
        parameter("$value", Type::int(), false),
    );
    method!(
        builder,
        collator,
        "getSortKey",
        union!(Type::string(), Type::false_type()),
        parameter("$string", Type::string(), false),
    );
    let number_formatter = builder.class("NumberFormatter", SymbolKind::Class);
    builder.class_constant(&number_formatter, "CURRENCY", Type::int());
    builder.class_constant(&number_formatter, "FRACTION_DIGITS", Type::int());
    method!(
        builder,
        number_formatter,
        "__construct",
        Type::void(),
        parameter("$locale", Type::string(), false),
        parameter("$style", Type::int(), false),
        parameter("$pattern", Type::nullable(Type::string()), true),
    );
    method!(
        builder,
        number_formatter,
        "setAttribute",
        Type::bool(),
        parameter("$attribute", Type::int(), false),
        parameter("$value", Type::int(), false),
    );
    method!(
        builder,
        number_formatter,
        "formatCurrency",
        union!(Type::string(), Type::false_type()),
        parameter("$amount", Type::float(), false),
        parameter("$currency", Type::string(), false),
    );
    let intl_date_formatter = builder.class("IntlDateFormatter", SymbolKind::Class);
    builder.class_constant(&intl_date_formatter, "MEDIUM", Type::int());
    method!(
        builder,
        intl_date_formatter,
        "__construct",
        Type::void(),
        parameter("$locale", Type::nullable(Type::string()), false),
        parameter("$dateType", Type::int(), false),
        parameter("$timeType", Type::int(), false),
    );
    method!(
        builder,
        intl_date_formatter,
        "format",
        union!(Type::string(), Type::false_type()),
        parameter(
            "$datetime",
            union!(named!("DateTimeInterface"), Type::int()),
            false
        ),
    );
    let locale = builder.class("Locale", SymbolKind::Class);
    static_method!(
        builder,
        locale,
        "canonicalize",
        Type::nullable(Type::string()),
        parameter("$locale", Type::string(), false),
    );
    static_method!(builder, locale, "getDefault", Type::string());
    static_method!(
        builder,
        locale,
        "parseLocale",
        Type::array(Type::string(), Type::string()),
        parameter("$locale", Type::string(), false),
    );

    let xml_reader = builder.class("XMLReader", SymbolKind::Class);
    for name in [
        "NONE",
        "ELEMENT",
        "ATTRIBUTE",
        "TEXT",
        "CDATA",
        "ENTITY_REF",
        "ENTITY",
        "PI",
        "COMMENT",
        "DOC",
        "DOC_TYPE",
        "WHITESPACE",
        "SIGNIFICANT_WHITESPACE",
        "END_ELEMENT",
        "END_ENTITY",
        "XML_DECLARATION",
    ] {
        builder.class_constant(&xml_reader, name, Type::int());
    }
    builder.property(&xml_reader, "nodeType", Type::int());
    builder.property(&xml_reader, "name", Type::string());
    builder.property(&xml_reader, "localName", Type::string());
    builder.property(&xml_reader, "namespaceURI", Type::string());
    builder.property(&xml_reader, "prefix", Type::string());
    builder.property(&xml_reader, "value", Type::string());
    builder.property(&xml_reader, "isEmptyElement", Type::bool());
    builder.property(&xml_reader, "hasAttributes", Type::bool());
    method!(
        builder,
        xml_reader,
        "open",
        Type::bool(),
        parameter("$uri", Type::string(), false),
        parameter("$encoding", Type::nullable(Type::string()), true),
        parameter("$flags", Type::int(), true),
    );
    method!(builder, xml_reader, "read", Type::bool());
    method!(builder, xml_reader, "close", Type::bool());
    method!(
        builder,
        xml_reader,
        "getAttribute",
        Type::nullable(Type::string()),
        parameter("$name", Type::string(), false),
    );
    method!(builder, xml_reader, "moveToElement", Type::bool());
    method!(builder, xml_reader, "moveToFirstAttribute", Type::bool());
    method!(builder, xml_reader, "moveToNextAttribute", Type::bool());
    method!(builder, xml_reader, "readInnerXml", Type::string());

    let zip_archive = builder.class("ZipArchive", SymbolKind::Class);
    for name in [
        "CREATE",
        "OVERWRITE",
        "ER_EXISTS",
        "ER_INCONS",
        "ER_INVAL",
        "ER_MEMORY",
        "ER_NOENT",
        "ER_NOZIP",
        "ER_OPEN",
        "ER_READ",
        "ER_SEEK",
    ] {
        builder.class_constant(&zip_archive, name, Type::int());
    }
    builder.property(&zip_archive, "numFiles", Type::int());
    builder.property(&zip_archive, "filename", Type::string());
    method!(
        builder,
        zip_archive,
        "open",
        union!(Type::true_type(), Type::int()),
        parameter("$filename", Type::string(), false),
        parameter("$flags", Type::int(), true),
    );
    method!(builder, zip_archive, "close", Type::bool());
    method!(
        builder,
        zip_archive,
        "addFromString",
        Type::bool(),
        parameter("$name", Type::string(), false),
        parameter("$content", Type::string(), false),
        parameter("$flags", Type::int(), true),
    );
    method!(
        builder,
        zip_archive,
        "extractTo",
        Type::bool(),
        parameter("$path", Type::string(), false),
        parameter(
            "$files",
            union!(
                Type::array(Type::array_key(), Type::string()),
                Type::string(),
                Type::null()
            ),
            true,
        ),
    );
    method!(
        builder,
        zip_archive,
        "getFromName",
        union!(Type::string(), Type::false_type()),
        parameter("$name", Type::string(), false),
        parameter("$length", Type::int(), true),
        parameter("$flags", Type::int(), true),
    );
    method!(
        builder,
        zip_archive,
        "statIndex",
        union!(
            Type::array(Type::string(), Type::mixed()),
            Type::false_type(),
        ),
        parameter("$index", Type::int(), false),
        parameter("$flags", Type::int(), true)
    );
    method!(
        builder,
        zip_archive,
        "statName",
        union!(
            Type::array(Type::string(), Type::mixed()),
            Type::false_type(),
        ),
        parameter("$name", Type::string(), false),
        parameter("$flags", Type::int(), true)
    );

    let reflection_attribute = builder.class("ReflectionAttribute", SymbolKind::Class);
    set_templates!(
        builder,
        reflection_attribute,
        template("TAttribute", Type::object(), Type::object()),
    );
    method!(builder, reflection_attribute, "getName", Type::string());
    method!(
        builder,
        reflection_attribute,
        "getArguments",
        Type::array(Type::array_key(), Type::mixed())
    );
    method!(
        builder,
        reflection_attribute,
        "newInstance",
        Type::template("TAttribute")
    );
    let attribute_method = |builder: &mut StubBuilder, container: &SymbolId| {
        method_with_templates!(
            builder,
            *container,
            "getAttributes",
            Type::list(named!("ReflectionAttribute", Type::template("TAttribute"),)),
            vec![template("TAttribute", Type::object(), Type::object())],
            parameter(
                "$name",
                Type::nullable(Type::class_string(Type::template("TAttribute"))),
                true,
            ),
            parameter("$flags", Type::int(), true),
        );
    };
    let reflection_type = builder.class("ReflectionType", SymbolKind::Class);
    method!(builder, reflection_type, "allowsNull", Type::bool());
    method!(builder, reflection_type, "__toString", Type::string());
    let reflection_named_type =
        builder.class_extends("ReflectionNamedType", SymbolKind::Class, "ReflectionType");
    method!(builder, reflection_named_type, "getName", Type::string());
    method!(builder, reflection_named_type, "isBuiltin", Type::bool());
    for name in ["ReflectionUnionType", "ReflectionIntersectionType"] {
        let reflection_composite = builder.class_extends(name, SymbolKind::Class, "ReflectionType");
        method!(
            builder,
            reflection_composite,
            "getTypes",
            Type::list(named!("ReflectionType")),
        );
    }
    let reflection_method = builder.class("ReflectionMethod", SymbolKind::Class);
    method!(
        builder,
        reflection_method,
        "__construct",
        Type::void(),
        parameter(
            "$objectOrMethod",
            union!(Type::object(), Type::string()),
            false,
        ),
        parameter("$method", Type::string(), true),
    );
    method!(builder, reflection_method, "getName", Type::string());
    method!(builder, reflection_method, "isPublic", Type::bool());
    method!(builder, reflection_method, "isPrivate", Type::bool());
    method!(builder, reflection_method, "isAbstract", Type::bool());
    method!(builder, reflection_method, "isStatic", Type::bool());
    method!(builder, reflection_method, "hasReturnType", Type::bool());
    method!(
        builder,
        reflection_method,
        "getDeclaringClass",
        named!("ReflectionClass"),
    );
    method!(
        builder,
        reflection_method,
        "getParameters",
        Type::list(named!("ReflectionParameter")),
    );
    method!(
        builder,
        reflection_method,
        "getReturnType",
        Type::nullable(named!("ReflectionType")),
    );
    method!(
        builder,
        reflection_method,
        "getDocComment",
        union!(Type::string(), Type::false_type()),
    );
    attribute_method(builder, &reflection_method);
    method!(
        builder,
        reflection_method,
        "invoke",
        Type::mixed(),
        parameter("$object", Type::nullable(Type::object()), false),
        variadic("$args", Type::mixed()),
    );
    for name in [
        "IS_STATIC",
        "IS_PUBLIC",
        "IS_PROTECTED",
        "IS_PRIVATE",
        "IS_ABSTRACT",
        "IS_FINAL",
    ] {
        builder.class_constant(&reflection_method, name, Type::int());
    }
    let reflection_parameter = builder.class("ReflectionParameter", SymbolKind::Class);
    method!(builder, reflection_parameter, "getName", Type::string());
    method!(
        builder,
        reflection_parameter,
        "getType",
        Type::nullable(named!("ReflectionType")),
    );
    method!(builder, reflection_parameter, "isOptional", Type::bool());
    method!(builder, reflection_parameter, "allowsNull", Type::bool());
    method!(builder, reflection_parameter, "isVariadic", Type::bool());
    method!(
        builder,
        reflection_parameter,
        "isDefaultValueAvailable",
        Type::bool()
    );
    method!(
        builder,
        reflection_parameter,
        "getDefaultValue",
        Type::mixed()
    );
    attribute_method(builder, &reflection_parameter);
    let reflection_property = builder.class("ReflectionProperty", SymbolKind::Class);
    method!(
        builder,
        reflection_property,
        "__construct",
        Type::void(),
        parameter("$class", union!(Type::object(), Type::string()), false,),
        parameter("$property", Type::string(), false),
    );
    method!(builder, reflection_property, "getName", Type::string());
    method!(builder, reflection_property, "isPublic", Type::bool());
    method!(builder, reflection_property, "isPrivate", Type::bool());
    method!(builder, reflection_property, "isReadOnly", Type::bool());
    method!(
        builder,
        reflection_property,
        "getType",
        Type::nullable(named!("ReflectionType")),
    );
    method!(
        builder,
        reflection_property,
        "getValue",
        Type::mixed(),
        parameter("$object", Type::nullable(Type::object()), true),
    );
    method!(
        builder,
        reflection_property,
        "setValue",
        Type::void(),
        parameter("$objectOrValue", Type::mixed(), false),
        parameter("$value", Type::mixed(), true),
    );
    method!(
        builder,
        reflection_property,
        "getDeclaringClass",
        named!("ReflectionClass"),
    );
    method!(
        builder,
        reflection_property,
        "getDocComment",
        union!(Type::string(), Type::false_type()),
    );
    attribute_method(builder, &reflection_property);
    let reflection_class = builder.class("ReflectionClass", SymbolKind::Class);
    builder.property(&reflection_class, "name", Type::string());
    set_templates!(
        builder,
        reflection_class,
        template("TObject", Type::object(), Type::object()),
    );
    method!(
        builder,
        reflection_class,
        "__construct",
        Type::void(),
        parameter(
            "$objectOrClass",
            union!(
                Type::template("TObject"),
                Type::class_string(Type::template("TObject")),
                Type::string(),
            ),
            false,
        ),
    );
    method!(builder, reflection_class, "getName", Type::string());
    method!(builder, reflection_class, "getShortName", Type::string());
    method!(
        builder,
        reflection_class,
        "getFileName",
        union!(Type::string(), Type::false_type()),
    );
    method!(
        builder,
        reflection_class,
        "getDocComment",
        union!(Type::string(), Type::false_type()),
    );
    method!(builder, reflection_class, "isAbstract", Type::bool());
    method!(builder, reflection_class, "isFinal", Type::bool());
    method!(builder, reflection_class, "isInterface", Type::bool());
    method!(builder, reflection_class, "isTrait", Type::bool());
    method!(builder, reflection_class, "isInstantiable", Type::bool());
    method!(
        builder,
        reflection_class,
        "hasMethod",
        Type::bool(),
        parameter("$name", Type::string(), false),
    );
    method!(
        builder,
        reflection_class,
        "hasProperty",
        Type::bool(),
        parameter("$name", Type::string(), false),
    );
    method!(
        builder,
        reflection_class,
        "hasConstant",
        Type::bool(),
        parameter("$name", Type::string(), false),
    );
    method!(
        builder,
        reflection_class,
        "implementsInterface",
        Type::bool(),
        parameter("$interface", Type::string(), false),
    );
    method!(
        builder,
        reflection_class,
        "isSubclassOf",
        Type::bool(),
        parameter("$class", Type::string(), false),
    );
    method!(
        builder,
        reflection_class,
        "getNamespaceName",
        Type::string()
    );
    method!(
        builder,
        reflection_class,
        "getParentClass",
        union!(named!("ReflectionClass"), Type::false_type()),
    );
    method!(
        builder,
        reflection_class,
        "getConstructor",
        Type::nullable(named!("ReflectionMethod")),
    );
    method!(
        builder,
        reflection_class,
        "getConstant",
        Type::mixed(),
        parameter("$name", Type::string(), false),
    );
    method!(
        builder,
        reflection_class,
        "isInstance",
        Type::bool(),
        parameter("$object", Type::object(), false),
    );
    attribute_method(builder, &reflection_class);
    method!(
        builder,
        reflection_class,
        "getMethods",
        Type::list(named!("ReflectionMethod")),
        parameter("$filter", Type::nullable(Type::int()), true),
    );
    method!(
        builder,
        reflection_class,
        "getProperties",
        Type::list(named!("ReflectionProperty")),
        parameter("$filter", Type::nullable(Type::int()), true),
    );
    method!(
        builder,
        reflection_class,
        "newInstance",
        Type::template("TObject"),
        variadic("$args", Type::mixed()),
    );
    method!(
        builder,
        reflection_class,
        "newInstanceArgs",
        Type::template("TObject"),
        parameter("$args", Type::array(Type::array_key(), Type::mixed()), true,),
    );
    method!(
        builder,
        reflection_class,
        "newInstanceWithoutConstructor",
        Type::template("TObject"),
    );
    method!(
        builder,
        reflection_class,
        "getMethod",
        named!("ReflectionMethod"),
        parameter("$name", Type::string(), false),
    );
    method!(
        builder,
        reflection_class,
        "getProperty",
        named!("ReflectionProperty"),
        parameter("$name", Type::string(), false),
    );
    let reflection_object =
        builder.class_extends("ReflectionObject", SymbolKind::Class, "ReflectionClass");
    method!(
        builder,
        reflection_object,
        "__construct",
        Type::void(),
        parameter("$object", Type::object(), false),
    );
    let reflection_enum =
        builder.class_extends("ReflectionEnum", SymbolKind::Class, "ReflectionClass");
    method!(
        builder,
        reflection_enum,
        "getBackingType",
        Type::nullable(named!("ReflectionNamedType")),
    );
    let reflection_function = builder.class("ReflectionFunction", SymbolKind::Class);
    method!(
        builder,
        reflection_function,
        "__construct",
        Type::void(),
        parameter(
            "$function",
            union!(named!("Closure"), Type::string(),),
            false
        ),
    );
    method!(
        builder,
        reflection_function,
        "getClosureThis",
        Type::nullable(Type::object()),
    );
    attribute_method(builder, &reflection_function);

    let redis = builder.class("Redis", SymbolKind::Class);
    for name in ["OPT_PREFIX", "MULTI"] {
        builder.class_constant(&redis, name, Type::int());
    }
    method!(
        builder,
        redis,
        "setOption",
        Type::bool(),
        parameter("$option", Type::int(), false),
        parameter("$value", Type::mixed(), false),
    );
    method!(
        builder,
        redis,
        "getOption",
        Type::mixed(),
        parameter("$option", Type::int(), false),
    );
    method!(builder, redis, "isConnected", Type::bool());
    let redis_result = union!(named!("Redis"), Type::int(), Type::false_type());
    method!(
        builder,
        redis,
        "del",
        redis_result.clone(),
        parameter(
            "$key",
            union!(Type::array(Type::mixed(), Type::mixed()), Type::string()),
            false
        ),
        variadic("$other_keys", Type::string()),
    );
    method!(
        builder,
        redis,
        "sAdd",
        redis_result,
        parameter("$key", Type::string(), false),
        parameter("$value", Type::mixed(), false),
        variadic("$other_values", Type::mixed()),
    );
    for name in ["sPop", "incr", "incrBy", "decr"] {
        method!(
            builder,
            redis,
            name,
            union!(Type::int(), Type::false_type()),
            parameter("$key", Type::string(), false),
            variadic("$values", Type::mixed()),
        );
    }
    method!(
        builder,
        redis,
        "get",
        Type::mixed(),
        parameter("$key", Type::string(), false),
    );
    method!(
        builder,
        redis,
        "set",
        union!(named!("Redis"), Type::string(), Type::bool()),
        parameter("$key", Type::string(), false),
        parameter("$value", Type::mixed(), false),
        parameter("$options", Type::mixed(), true),
    );
    method!(
        builder,
        redis,
        "keys",
        union!(
            named!("Redis"),
            Type::array(Type::mixed(), Type::mixed()),
            Type::false_type(),
        ),
        parameter("$pattern", Type::string(), false)
    );
    method!(
        builder,
        redis,
        "sMembers",
        union!(
            named!("Redis"),
            Type::array(Type::mixed(), Type::mixed()),
            Type::false_type(),
        ),
        parameter("$key", Type::string(), false)
    );
    method!(
        builder,
        redis,
        "multi",
        union!(named!("Redis"), Type::bool()),
        parameter("$value", Type::int(), true),
    );
    method!(
        builder,
        redis,
        "exec",
        union!(
            named!("Redis"),
            Type::array(Type::mixed(), Type::mixed()),
            Type::false_type(),
        )
    );
    method!(
        builder,
        redis,
        "mget",
        Type::mixed(),
        parameter("$key", Type::mixed(), false),
        variadic("$values", Type::mixed()),
    );
    method!(
        builder,
        redis,
        "watch",
        Type::mixed(),
        variadic("$args", Type::mixed())
    );
    builder.class_extends("RedisException", SymbolKind::Class, "RuntimeException");

    let imagick_pixel = builder.class("ImagickPixel", SymbolKind::Class);
    method!(
        builder,
        imagick_pixel,
        "__construct",
        Type::void(),
        parameter("$color", Type::string(), true),
    );
    let imagick = builder.class("Imagick", SymbolKind::Class);
    for name in ["FILTER_LANCZOS", "COMPOSITE_OVER", "INTERLACE_JPEG"] {
        builder.class_constant(&imagick, name, Type::int());
    }
    method!(builder, imagick, "__construct", Type::void());
    method!(
        builder,
        imagick,
        "readImageBlob",
        Type::bool(),
        parameter("$image", Type::string(), false),
    );
    method!(
        builder,
        imagick,
        "rotateImage",
        Type::bool(),
        parameter("$background", named!("ImagickPixel"), false),
        parameter("$degrees", Type::float(), false),
    );
    method!(builder, imagick, "getImageWidth", Type::int());
    method!(builder, imagick, "getImageHeight", Type::int());
    method!(
        builder,
        imagick,
        "resizeImage",
        Type::bool(),
        parameter("$columns", Type::int(), false),
        parameter("$rows", Type::int(), false),
        parameter("$filter", Type::int(), false),
        parameter("$blur", Type::float(), false),
    );
    method!(
        builder,
        imagick,
        "newImage",
        Type::bool(),
        parameter("$columns", Type::int(), false),
        parameter("$rows", Type::int(), false),
        parameter("$background", named!("ImagickPixel"), false),
    );
    method!(builder, imagick, "getImageFormat", Type::string());
    method!(
        builder,
        imagick,
        "setImageFormat",
        Type::bool(),
        parameter("$format", Type::string(), false),
    );
    method!(
        builder,
        imagick,
        "compositeImage",
        Type::bool(),
        parameter("$compositeImage", named!("Imagick"), false),
        parameter("$composite", Type::int(), false),
        parameter("$x", Type::int(), false),
        parameter("$y", Type::int(), false),
    );
    method!(builder, imagick, "clear", Type::bool());
    method!(
        builder,
        imagick,
        "setImageCompressionQuality",
        Type::bool(),
        parameter("$quality", Type::int(), false),
    );
    method!(
        builder,
        imagick,
        "setInterlaceScheme",
        Type::bool(),
        parameter("$interlace", Type::int(), false),
    );
    static_method!(
        builder,
        imagick,
        "queryFormats",
        Type::list(Type::string()),
        parameter("$pattern", Type::string(), true),
    );
    method!(builder, imagick, "getImageBlob", Type::string());

    let array_object = builder.class("ArrayObject", SymbolKind::Class);
    builder.add_implements(
        &array_object,
        &["IteratorAggregate", "ArrayAccess", "Countable"],
    );
    method!(builder, array_object, "count", Type::int());
    method!(builder, array_object, "getIterator", named!("Iterator"));
    method!(
        builder,
        array_object,
        "getArrayCopy",
        Type::array(Type::array_key(), Type::mixed())
    );

    let attribute = builder.class("Attribute", SymbolKind::Class);
    builder.class_constant(&attribute, "TARGET_CLASS", Type::int());
    builder.class_constant(&attribute, "TARGET_FUNCTION", Type::int());
    builder.class_constant(&attribute, "TARGET_METHOD", Type::int());
    builder.class_constant(&attribute, "TARGET_PROPERTY", Type::int());
    builder.class_constant(&attribute, "TARGET_CLASS_CONSTANT", Type::int());
    builder.class_constant(&attribute, "TARGET_PARAMETER", Type::int());
    builder.class_constant(&attribute, "TARGET_ALL", Type::int());
    builder.class_constant(&attribute, "IS_REPEATABLE", Type::int());
    method!(
        builder,
        attribute,
        "__construct",
        Type::void(),
        parameter("$flags", Type::int(), true)
    );
    builder.class("AllowDynamicProperties", SymbolKind::Class);
    builder.class("ReturnTypeWillChange", SymbolKind::Class);
    builder.class("SensitiveParameter", SymbolKind::Class);
    if version.at_least(8, 3) {
        builder.class("Override", SymbolKind::Class);
    }

    let generator = builder.class_extends("Generator", SymbolKind::Class, "Iterator");
    method!(builder, generator, "getReturn", Type::mixed());
    method!(
        builder,
        generator,
        "send",
        Type::mixed(),
        parameter("$value", Type::mixed(), false)
    );
    method!(
        builder,
        generator,
        "throw",
        Type::mixed(),
        parameter("$exception", named!("Throwable"), false)
    );
    if version.at_least(8, 1) {
        let fiber = builder.class("Fiber", SymbolKind::Class);
        method!(
            builder,
            fiber,
            "__construct",
            Type::void(),
            parameter(
                "$callback",
                Type::callable(Vec::new(), Type::mixed()),
                false
            )
        );
        method!(
            builder,
            fiber,
            "start",
            Type::mixed(),
            variadic("$args", Type::mixed())
        );
        method!(
            builder,
            fiber,
            "resume",
            Type::mixed(),
            parameter("$value", Type::mixed(), true)
        );
        method!(builder, fiber, "isStarted", Type::bool());
        method!(builder, fiber, "isTerminated", Type::bool());
        method!(builder, fiber, "getReturn", Type::mixed());
    }

    function!(
        builder,
        "count",
        Type::int(),
        parameter(
            "$value",
            union!(
                Type::array(Type::mixed(), Type::mixed()),
                named!("Countable")
            ),
            false
        )
    );
    function!(
        builder,
        "strlen",
        Type::int(),
        parameter("$string", Type::string(), false)
    );
    function!(
        builder,
        "substr",
        Type::string(),
        parameter("$string", Type::string(), false),
        parameter("$offset", Type::int(), false),
        parameter("$length", Type::nullable(Type::int()), true),
    );
    function!(
        builder,
        "strpos",
        union!(Type::int(), Type::false_type()),
        parameter("$haystack", Type::string(), false),
        parameter("$needle", Type::string(), false),
        parameter("$offset", Type::int(), true),
    );
    function!(
        builder,
        "mb_strlen",
        Type::int(),
        parameter("$string", Type::string(), false),
        parameter("$encoding", Type::nullable(Type::string()), true),
    );
    function!(
        builder,
        "mb_substr",
        Type::string(),
        parameter("$string", Type::string(), false),
        parameter("$start", Type::int(), false),
        parameter("$length", Type::nullable(Type::int()), true),
        parameter("$encoding", Type::nullable(Type::string()), true),
    );
    function!(
        builder,
        "mb_strpos",
        union!(Type::int(), Type::false_type()),
        parameter("$haystack", Type::string(), false),
        parameter("$needle", Type::string(), false),
        parameter("$offset", Type::int(), true),
        parameter("$encoding", Type::nullable(Type::string()), true),
    );
    function!(
        builder,
        "sprintf",
        Type::string(),
        parameter("$format", Type::string(), false),
        variadic("$values", Type::mixed())
    );
    function!(
        builder,
        "json_encode",
        union!(Type::string(), Type::false_type()),
        parameter("$value", Type::mixed(), false),
        parameter("$flags", Type::int(), true),
        parameter("$depth", Type::int(), true),
    );
    function!(
        builder,
        "json_decode",
        Type::mixed(),
        parameter("$json", Type::string(), false),
        parameter("$associative", Type::nullable(Type::bool()), true),
        parameter("$depth", Type::int(), true),
        parameter("$flags", Type::int(), true),
    );
    function!(
        builder,
        "preg_match",
        union!(Type::int(), Type::false_type()),
        parameter("$pattern", Type::string(), false),
        parameter("$subject", Type::string(), false),
        by_reference(
            "$matches",
            Type::array(Type::array_key(), Type::mixed()),
            true,
        ),
        parameter("$flags", Type::int(), true),
        parameter("$offset", Type::int(), true),
    );
    function!(
        builder,
        "preg_match_all",
        union!(Type::int(), Type::false_type()),
        parameter("$pattern", Type::string(), false),
        parameter("$subject", Type::string(), false),
        by_reference(
            "$matches",
            Type::array(Type::array_key(), Type::mixed()),
            true,
        ),
        parameter("$flags", Type::int(), true),
        parameter("$offset", Type::int(), true),
    );
    function!(
        builder,
        "parse_str",
        Type::void(),
        parameter("$string", Type::string(), false),
        by_reference(
            "$result",
            Type::array(Type::array_key(), Type::mixed()),
            true,
        ),
    );
    function!(
        builder,
        "get_class",
        Type::class_string(Type::object()),
        parameter("$object", Type::object(), true)
    );
    function!(
        builder,
        "class_exists",
        Type::bool(),
        parameter("$class", Type::string(), false),
        parameter("$autoload", Type::bool(), true),
    );
    function!(
        builder,
        "interface_exists",
        Type::bool(),
        parameter("$interface", Type::string(), false),
        parameter("$autoload", Type::bool(), true),
    );
    function!(
        builder,
        "is_a",
        Type::bool(),
        parameter(
            "$object_or_class",
            union!(Type::object(), Type::string()),
            false
        ),
        parameter("$class", Type::string(), false),
        parameter("$allow_string", Type::bool(), true),
    );
    function!(
        builder,
        "in_array",
        Type::bool(),
        parameter("$needle", Type::mixed(), false),
        parameter(
            "$haystack",
            Type::array(Type::array_key(), Type::mixed()),
            false
        ),
        parameter("$strict", Type::bool(), true),
    );
    function!(
        builder,
        "array_values",
        Type::list(Type::template("T")),
        parameter(
            "$array",
            Type::array(Type::array_key(), Type::template("T")),
            false
        )
    );
    function!(
        builder,
        "array_keys",
        Type::list(Type::template("TKey")),
        parameter(
            "$array",
            Type::array(Type::template("TKey"), Type::mixed()),
            false
        )
    );
    function!(
        builder,
        "array_filter",
        Type::array(Type::template("TKey"), Type::template("TValue")),
        parameter(
            "$array",
            Type::array(Type::template("TKey"), Type::template("TValue")),
            false
        ),
        parameter(
            "$callback",
            Type::nullable(Type::callable(Vec::new(), Type::mixed())),
            true
        ),
        parameter("$mode", Type::int(), true),
    );
    if version.at_least(8, 0) {
        function!(
            builder,
            "str_contains",
            Type::bool(),
            parameter("$haystack", Type::string(), false),
            parameter("$needle", Type::string(), false),
        );
    }
    if version.at_least(8, 3) {
        function!(
            builder,
            "json_validate",
            Type::bool(),
            parameter("$json", Type::string(), false)
        );
    }
}
