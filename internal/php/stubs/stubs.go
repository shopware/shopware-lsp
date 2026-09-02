// Package stubs supplies version-aware semantic declarations for PHP runtime
// symbols that do not exist in project source.
package stubs

import (
	"strings"

	"github.com/shopware/shopware-lsp/internal/php/project"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/php/types"
)

func Document(version project.Version) *semantic.Document {
	return document(version, nil)
}

func DocumentForExtensions(
	version project.Version,
	extensions []string,
	disabled ...[]string,
) *semantic.Document {
	return document(version, SelectedExtensions(extensions, disabled...))
}

func document(version project.Version, extensions []string) *semantic.Document {
	path := "phpstub://" + version.String() + "/core"
	generated := generatedSymbols(version, path)
	if extensions != nil {
		path = "phpstub://" + version.String() + "/selected"
		generated = generatedSymbolsForExtensions(version, path, extensions)
	}
	builder := newStubBuilder(path, generated)
	traceFrame := types.ArrayShape([]types.ShapeField{
		{Name: "args", Type: types.List(types.Mixed()), Optional: true},
		{Name: "class", Type: types.ClassString(types.Object()), Optional: true},
		{Name: "file", Type: types.String(), Optional: true},
		{Name: "function", Type: types.String()},
		{Name: "line", Type: types.Int(), Optional: true},
		{Name: "object", Type: types.Object(), Optional: true},
		{Name: "type", Type: types.String(), Optional: true},
	}, true)

	addThrowableMethods := func(container semantic.SymbolID) {
		builder.method(container, "getMessage", types.String())
		builder.method(container, "getCode", types.Int())
		builder.method(container, "getFile", types.String())
		builder.method(container, "getLine", types.Int())
		builder.method(
			container,
			"getTrace",
			types.List(traceFrame),
		)
		builder.method(container, "getTraceAsString", types.String())
		builder.method(
			container,
			"getPrevious",
			types.Nullable(types.Named("Throwable")),
		)
		builder.method(container, "__toString", types.String())
	}
	throwable := builder.class("Throwable", semantic.InterfaceSymbol)
	addThrowableMethods(throwable)
	exception := builder.class("Exception", semantic.ClassSymbol)
	builder.addImplements(exception, "Throwable")
	addThrowableMethods(exception)
	errorClass := builder.class("Error", semantic.ClassSymbol)
	builder.addImplements(errorClass, "Throwable")
	addThrowableMethods(errorClass)
	for _, container := range []semantic.SymbolID{exception, errorClass} {
		builder.propertyVisibility(
			container,
			"message",
			types.String(),
			semantic.Protected,
		)
		builder.propertyVisibility(
			container,
			"code",
			types.Int(),
			semantic.Protected,
		)
		builder.propertyVisibility(
			container,
			"file",
			types.String(),
			semantic.Protected,
		)
		builder.propertyVisibility(
			container,
			"line",
			types.Int(),
			semantic.Protected,
		)
	}
	builder.classExtends("RuntimeException", semantic.ClassSymbol, "Exception")
	builder.classExtends("LogicException", semantic.ClassSymbol, "Exception")
	builder.classExtends("InvalidArgumentException", semantic.ClassSymbol, "LogicException")
	builder.classExtends("BadFunctionCallException", semantic.ClassSymbol, "LogicException")
	builder.classExtends("BadMethodCallException", semantic.ClassSymbol, "BadFunctionCallException")
	builder.classExtends("DomainException", semantic.ClassSymbol, "LogicException")
	builder.classExtends("LengthException", semantic.ClassSymbol, "LogicException")
	builder.classExtends("OutOfRangeException", semantic.ClassSymbol, "LogicException")
	builder.classExtends("OutOfBoundsException", semantic.ClassSymbol, "RuntimeException")
	builder.classExtends("OverflowException", semantic.ClassSymbol, "RuntimeException")
	builder.classExtends("RangeException", semantic.ClassSymbol, "RuntimeException")
	builder.classExtends("UnderflowException", semantic.ClassSymbol, "RuntimeException")
	builder.classExtends("UnexpectedValueException", semantic.ClassSymbol, "RuntimeException")
	builder.classExtends("ErrorException", semantic.ClassSymbol, "Exception")
	builder.classExtends("TypeError", semantic.ClassSymbol, "Error")
	builder.classExtends("ArgumentCountError", semantic.ClassSymbol, "TypeError")
	builder.classExtends("ValueError", semantic.ClassSymbol, "Error")
	builder.classExtends("ArithmeticError", semantic.ClassSymbol, "Error")
	builder.classExtends("DivisionByZeroError", semantic.ClassSymbol, "ArithmeticError")
	builder.classExtends("AssertionError", semantic.ClassSymbol, "Error")
	builder.classExtends("ParseError", semantic.ClassSymbol, "Error")
	builder.classExtends("UnhandledMatchError", semantic.ClassSymbol, "Error")
	builder.classExtends("JsonException", semantic.ClassSymbol, "Exception")
	builder.classExtends("ReflectionException", semantic.ClassSymbol, "Exception")
	builder.method(exception, "__construct", types.Void(),
		parameter("$message", types.String(), true),
		parameter("$code", types.Int(), true),
		parameter("$previous", types.Nullable(types.Named("Throwable")), true),
	)
	builder.method(errorClass, "__construct", types.Void(),
		parameter("$message", types.String(), true),
		parameter("$code", types.Int(), true),
		parameter("$previous", types.Nullable(types.Named("Throwable")), true),
	)
	builder.class("Traversable", semantic.InterfaceSymbol)
	iterator := builder.classExtends("Iterator", semantic.InterfaceSymbol, "Traversable")
	builder.method(iterator, "current", types.Mixed())
	builder.method(iterator, "key", types.Mixed())
	builder.method(iterator, "next", types.Void())
	builder.method(iterator, "valid", types.Bool())
	iteratorAggregate := builder.classExtends("IteratorAggregate", semantic.InterfaceSymbol, "Traversable")
	builder.method(iteratorAggregate, "getIterator", types.Named("Traversable"))
	countable := builder.class("Countable", semantic.InterfaceSymbol)
	builder.method(countable, "count", types.Int())
	arrayAccess := builder.class("ArrayAccess", semantic.InterfaceSymbol)
	builder.method(arrayAccess, "offsetGet", types.Mixed(), parameter("$offset", types.Mixed(), false))
	builder.method(arrayAccess, "offsetExists", types.Bool(), parameter("$offset", types.Mixed(), false))
	stringable := builder.class("Stringable", semantic.InterfaceSymbol)
	builder.method(stringable, "__toString", types.String())
	jsonSerializable := builder.class("JsonSerializable", semantic.InterfaceSymbol)
	builder.method(jsonSerializable, "jsonSerialize", types.Mixed())
	builder.class("stdClass", semantic.ClassSymbol)
	weakMap := builder.class("WeakMap", semantic.ClassSymbol)
	builder.addImplements(weakMap, "IteratorAggregate", "ArrayAccess", "Countable")
	splFileInfo := builder.class("SplFileInfo", semantic.ClassSymbol)
	builder.method(splFileInfo, "__construct", types.Void(),
		parameter("$filename", types.String(), false),
	)
	builder.method(splFileInfo, "getPathname", types.String())
	builder.method(splFileInfo, "getPath", types.String())
	builder.method(splFileInfo, "getFilename", types.String())
	builder.method(splFileInfo, "getBasename", types.String(),
		parameter("$suffix", types.String(), true),
	)
	builder.method(splFileInfo, "getExtension", types.String())
	builder.method(splFileInfo, "getRealPath", types.Union(types.String(), types.False()))
	builder.method(splFileInfo, "getSize", types.Int())
	builder.method(splFileInfo, "getPerms", types.Union(types.Int(), types.False()))
	builder.method(splFileInfo, "isFile", types.Bool())
	builder.method(splFileInfo, "isDir", types.Bool())
	splFileObject := builder.classExtends(
		"SplFileObject",
		semantic.ClassSymbol,
		"SplFileInfo",
	)
	builder.addImplements(splFileObject, "Iterator")
	builder.method(splFileObject, "__construct", types.Void(),
		parameter("$filename", types.String(), false),
		parameter("$mode", types.String(), true),
		parameter("$useIncludePath", types.Bool(), true),
		parameter("$context", types.Resource(), true),
	)
	builder.method(splFileObject, "seek", types.Void(),
		parameter("$line", types.Int(), false),
	)
	iteratorIterator := builder.class("IteratorIterator", semantic.ClassSymbol)
	builder.addImplements(iteratorIterator, "Iterator")
	callbackFilterIterator := builder.classExtends(
		"CallbackFilterIterator",
		semantic.ClassSymbol,
		"IteratorIterator",
	)
	builder.method(callbackFilterIterator, "__construct", types.Void(),
		parameter("$iterator", types.Named("Iterator"), false),
		parameter("$callback", types.Callable(nil, types.Bool()), false),
	)
	filesystemIterator := builder.classExtends(
		"FilesystemIterator",
		semantic.ClassSymbol,
		"IteratorIterator",
	)
	builder.classConstant(filesystemIterator, "SKIP_DOTS", types.Int())
	builder.method(filesystemIterator, "__construct", types.Void(),
		parameter("$directory", types.String(), false),
		parameter("$flags", types.Int(), true),
	)
	builder.classExtends(
		"RecursiveDirectoryIterator",
		semantic.ClassSymbol,
		"FilesystemIterator",
	)
	recursiveIteratorIterator := builder.class(
		"RecursiveIteratorIterator",
		semantic.ClassSymbol,
	)
	builder.addImplements(recursiveIteratorIterator, "Iterator")
	builder.method(splFileObject, "eof", types.Bool())

	arrayIterator := builder.class("ArrayIterator", semantic.ClassSymbol)
	builder.setTemplates(
		arrayIterator,
		semantic.TemplateParameter{
			Name:    "TKey",
			Bound:   types.ArrayKey(),
			Default: types.ArrayKey(),
		},
		semantic.TemplateParameter{
			Name:    "TValue",
			Bound:   types.Mixed(),
			Default: types.Mixed(),
		},
	)
	builder.addImplements(arrayIterator, "Iterator", "ArrayAccess", "Countable")
	builder.method(arrayIterator, "__construct", types.Void(),
		parameter(
			"$array",
			types.Union(
				types.Array(
					types.Template("TKey"),
					types.Template("TValue"),
				),
				types.Object(),
			),
			true,
		),
		parameter("$flags", types.Int(), true),
	)
	builder.method(arrayIterator, "getArrayCopy", types.Array(types.ArrayKey(), types.Mixed()))
	builder.method(arrayIterator, "append", types.Void(),
		parameter("$value", types.Mixed(), false),
	)

	unitEnum := builder.class("UnitEnum", semantic.InterfaceSymbol)
	builder.staticMethod(
		unitEnum,
		"cases",
		types.List(types.Static()),
	)
	backedEnum := builder.classExtends("BackedEnum", semantic.InterfaceSymbol, "UnitEnum")
	builder.staticMethod(
		backedEnum,
		"from",
		types.Static(),
		parameter("$value", types.Union(types.Int(), types.String()), false),
	)
	builder.staticMethod(
		backedEnum,
		"tryFrom",
		types.Nullable(types.Static()),
		parameter("$value", types.Union(types.Int(), types.String()), false),
	)

	closure := builder.class("Closure", semantic.ClassSymbol)
	builder.method(closure, "call", types.Mixed(),
		parameter("$newThis", types.Object(), false),
		variadic("$args", types.Mixed()),
	)
	builder.method(closure, "bindTo", types.Nullable(types.Named("Closure")),
		parameter("$newThis", types.Nullable(types.Object()), false),
		parameter("$newScope", types.Union(types.Object(), types.String(), types.Null()), true),
	)
	closureTemplate := types.Template("TClosure")
	builder.staticMethodWithTemplates(closure, "bind", types.Nullable(closureTemplate), []semantic.TemplateParameter{{
		Name:  "TClosure",
		Bound: types.Callable(nil, types.Mixed()),
	}},
		parameter("$closure", closureTemplate, false),
		parameter("$newThis", types.Nullable(types.Object()), false),
		parameter("$newScope", types.Union(types.Object(), types.String(), types.Null()), true),
	)
	builder.staticMethod(
		closure,
		"fromCallable",
		types.Named("Closure"),
		parameter("$callback", types.Callable(nil, types.Mixed()), false),
	)

	dateTime := builder.class("DateTimeInterface", semantic.InterfaceSymbol)
	builder.classConstant(dateTime, "ATOM", types.String())
	builder.classConstant(dateTime, "RFC3339_EXTENDED", types.String())
	builder.method(dateTime, "format", types.String(), parameter("$format", types.String(), false))
	builder.method(dateTime, "getTimestamp", types.Int())
	builder.method(dateTime, "diff", types.Named("DateInterval"),
		parameter("$targetObject", types.Named("DateTimeInterface"), false),
		parameter("$absolute", types.Bool(), true),
	)
	dateTimeZone := builder.class("DateTimeZone", semantic.ClassSymbol)
	builder.classConstant(dateTimeZone, "ALL_WITH_BC", types.Int())
	builder.method(dateTimeZone, "__construct", types.Void(),
		parameter("$timezone", types.String(), false),
	)
	builder.staticMethod(
		dateTimeZone,
		"listIdentifiers",
		types.List(types.String()),
		parameter("$timezoneGroup", types.Int(), true),
		parameter("$countryCode", types.Nullable(types.String()), true),
	)
	dateTimeMutable := builder.classExtends("DateTime", semantic.ClassSymbol, "DateTimeInterface")
	builder.method(dateTimeMutable, "__construct", types.Void(),
		parameter("$datetime", types.String(), true),
		parameter("$timezone", types.Nullable(types.Named("DateTimeZone")), true),
	)
	builder.method(dateTimeMutable, "modify", types.Union(types.Static(), types.False()),
		parameter("$modifier", types.String(), false),
	)
	builder.method(dateTimeMutable, "add", types.Static(),
		parameter("$interval", types.Named("DateInterval"), false),
	)
	builder.method(dateTimeMutable, "sub", types.Static(),
		parameter("$interval", types.Named("DateInterval"), false),
	)
	builder.method(dateTimeMutable, "setTime", types.Static(),
		parameter("$hour", types.Int(), false),
		parameter("$minute", types.Int(), false),
		parameter("$second", types.Int(), true),
		parameter("$microsecond", types.Int(), true),
	)
	builder.method(dateTimeMutable, "setTimezone", types.Static(),
		parameter("$timezone", types.Named("DateTimeZone"), false),
	)
	builder.method(dateTimeMutable, "setTimestamp", types.Static(),
		parameter("$timestamp", types.Int(), false),
	)
	builder.staticMethod(dateTimeMutable, "createFromImmutable", types.Static(),
		parameter("$object", types.Named("DateTimeImmutable"), false),
	)
	builder.staticMethod(dateTimeMutable, "createFromFormat", types.Union(types.Static(), types.False()),
		parameter("$format", types.String(), false),
		parameter("$datetime", types.String(), false),
		parameter("$timezone", types.Nullable(types.Named("DateTimeZone")), true),
	)
	dateTimeImmutable := builder.classExtends("DateTimeImmutable", semantic.ClassSymbol, "DateTimeInterface")
	builder.method(dateTimeImmutable, "__construct", types.Void(),
		parameter("$datetime", types.String(), true),
		parameter("$timezone", types.Nullable(types.Named("DateTimeZone")), true),
	)
	builder.method(dateTimeImmutable, "modify", types.Union(types.Static(), types.False()),
		parameter("$modifier", types.String(), false),
	)
	builder.method(dateTimeImmutable, "add", types.Static(),
		parameter("$interval", types.Named("DateInterval"), false),
	)
	builder.method(dateTimeImmutable, "sub", types.Static(),
		parameter("$interval", types.Named("DateInterval"), false),
	)
	builder.method(dateTimeImmutable, "setTime", types.Static(),
		parameter("$hour", types.Int(), false),
		parameter("$minute", types.Int(), false),
		parameter("$second", types.Int(), true),
		parameter("$microsecond", types.Int(), true),
	)
	builder.method(dateTimeImmutable, "setTimezone", types.Static(),
		parameter("$timezone", types.Named("DateTimeZone"), false),
	)
	builder.method(dateTimeImmutable, "setTimestamp", types.Static(),
		parameter("$timestamp", types.Int(), false),
	)
	builder.staticMethod(dateTimeImmutable, "createFromInterface", types.Static(),
		parameter("$object", types.Named("DateTimeInterface"), false),
	)
	builder.staticMethod(dateTimeImmutable, "createFromMutable", types.Static(),
		parameter("$object", types.Named("DateTime"), false),
	)
	builder.staticMethod(dateTimeImmutable, "createFromFormat", types.Union(types.Static(), types.False()),
		parameter("$format", types.String(), false),
		parameter("$datetime", types.String(), false),
		parameter("$timezone", types.Nullable(types.Named("DateTimeZone")), true),
	)
	dateInterval := builder.class("DateInterval", semantic.ClassSymbol)
	builder.method(dateInterval, "__construct", types.Void(),
		parameter("$duration", types.String(), false),
	)
	builder.staticMethod(
		dateInterval,
		"createFromDateString",
		types.Union(types.Named("DateInterval"), types.False()),
		parameter("$datetime", types.String(), false),
	)
	builder.method(dateInterval, "format", types.String(),
		parameter("$format", types.String(), false),
	)
	for _, name := range []string{"y", "m", "d", "h", "i", "s", "f", "invert", "days"} {
		value := types.Int()
		switch name {
		case "f":
			value = types.Float()
		case "days":
			value = types.Union(types.Int(), types.False())
		}
		builder.property(dateInterval, name, value)
	}

	pdo := builder.class("PDO", semantic.ClassSymbol)
	for _, name := range []string{
		"ATTR_STRINGIFY_FETCHES", "ATTR_TIMEOUT", "PARAM_BOOL", "PARAM_INT",
		"PARAM_NULL", "PARAM_STR", "ATTR_PERSISTENT",
	} {
		builder.classConstant(pdo, name, types.Int())
	}
	pdoMysql := builder.class("Pdo\\Mysql", semantic.ClassSymbol)
	for _, name := range []string{
		"ATTR_INIT_COMMAND", "ATTR_SSL_CA", "ATTR_SSL_CERT", "ATTR_SSL_KEY",
		"ATTR_SSL_VERIFY_SERVER_CERT", "ATTR_COMPRESS",
	} {
		builder.classConstant(pdoMysql, name, types.Int())
	}

	domNode := builder.class("DOMNode", semantic.ClassSymbol)
	builder.property(domNode, "nodeName", types.String())
	builder.property(domNode, "nodeValue", types.Nullable(types.String()))
	builder.property(domNode, "localName", types.Nullable(types.String()))
	builder.property(domNode, "textContent", types.String())
	builder.property(
		domNode,
		"childNodes",
		types.Named("DOMNodeList", types.Named("DOMNode")),
	)
	builder.method(domNode, "appendChild", types.Union(types.Named("DOMNode"), types.False()),
		parameter("$node", types.Named("DOMNode"), false),
	)
	builder.method(domNode, "hasChildNodes", types.Bool())
	domElement := builder.classExtends("DOMElement", semantic.ClassSymbol, "DOMNode")
	builder.property(domElement, "tagName", types.String())
	builder.property(domElement, "attributes", types.Named("DOMNamedNodeMap"))
	builder.method(domElement, "getAttribute", types.String(),
		parameter("$qualifiedName", types.String(), false),
	)
	builder.method(domElement, "hasAttribute", types.Bool(),
		parameter("$qualifiedName", types.String(), false),
	)
	builder.method(domElement, "setAttribute", types.Union(types.Bool(), types.Named("DOMAttr")),
		parameter("$qualifiedName", types.String(), false),
		parameter("$value", types.String(), false),
	)
	domNodeList := builder.class("DOMNodeList", semantic.ClassSymbol)
	builder.setTemplates(
		domNodeList,
		semantic.TemplateParameter{
			Name:    "TNode",
			Bound:   types.Named("DOMNode"),
			Default: types.Named("DOMNode"),
		},
	)
	builder.addImplements(domNodeList, "IteratorAggregate", "Countable")
	builder.property(domNodeList, "length", types.Int())
	builder.method(domNodeList, "item", types.Nullable(types.Template("TNode")),
		parameter("$index", types.Int(), false),
	)
	builder.method(domNodeList, "count", types.Int())
	builder.method(domNodeList, "getIterator", types.Named("Iterator"))
	builder.method(
		domElement,
		"getElementsByTagName",
		types.Named("DOMNodeList", types.Named("DOMElement")),
		parameter("$qualifiedName", types.String(), false),
	)
	domAttr := builder.classExtends("DOMAttr", semantic.ClassSymbol, "DOMNode")
	builder.property(domAttr, "name", types.String())
	builder.property(domAttr, "value", types.String())
	domNamedNodeMap := builder.class("DOMNamedNodeMap", semantic.ClassSymbol)
	builder.addImplements(domNamedNodeMap, "IteratorAggregate", "Countable")
	builder.method(domNamedNodeMap, "getIterator", types.Named("Iterator"))
	builder.method(domNamedNodeMap, "count", types.Int())
	builder.classExtends("DOMText", semantic.ClassSymbol, "DOMNode")
	domDocument := builder.classExtends(
		"DOMDocument",
		semantic.ClassSymbol,
		"DOMNode",
	)
	builder.property(domDocument, "formatOutput", types.Bool())
	builder.property(domDocument, "preserveWhiteSpace", types.Bool())
	builder.property(domDocument, "encoding", types.Nullable(types.String()))
	builder.method(domDocument, "__construct", types.Void(),
		parameter("$version", types.String(), true),
		parameter("$encoding", types.String(), true),
	)
	builder.method(domDocument, "loadXML", types.Bool(),
		parameter("$source", types.String(), false),
		parameter("$options", types.Int(), true),
	)
	builder.method(domDocument, "load", types.Bool(),
		parameter("$filename", types.String(), false),
		parameter("$options", types.Int(), true),
	)
	builder.method(domDocument, "saveXML", types.Union(types.String(), types.False()),
		parameter("$node", types.Nullable(types.Named("DOMNode")), true),
		parameter("$options", types.Int(), true),
	)
	builder.method(domDocument, "createElement", types.Named("DOMElement"),
		parameter("$localName", types.String(), false),
		parameter("$value", types.String(), true),
	)
	builder.method(domDocument, "createTextNode", types.Named("DOMText"),
		parameter("$data", types.String(), false),
	)
	builder.method(
		domDocument,
		"getElementsByTagName",
		types.Named("DOMNodeList", types.Named("DOMElement")),
		parameter("$qualifiedName", types.String(), false),
	)
	domXPath := builder.class("DOMXPath", semantic.ClassSymbol)
	builder.method(domXPath, "__construct", types.Void(),
		parameter("$document", types.Named("DOMDocument"), false),
		parameter("$registerNodeNS", types.Bool(), true),
	)
	builder.method(
		domXPath,
		"query",
		types.Union(
			types.Named("DOMNodeList", types.Named("DOMNode")),
			types.False(),
		),
		parameter("$expression", types.String(), false),
		parameter("$contextNode", types.Nullable(types.Named("DOMNode")), true),
		parameter("$registerNodeNS", types.Bool(), true),
	)
	libxmlError := builder.class("LibXMLError", semantic.ClassSymbol)
	builder.property(libxmlError, "column", types.Int())
	builder.property(libxmlError, "line", types.Int())
	builder.property(libxmlError, "message", types.String())
	simpleXML := builder.class("SimpleXMLElement", semantic.ClassSymbol)
	builder.method(simpleXML, "__get", types.Mixed(),
		parameter("$name", types.String(), false),
	)
	builder.method(simpleXML, "attributes", types.Nullable(types.Named("SimpleXMLElement")),
		parameter("$namespaceOrPrefix", types.Nullable(types.String()), true),
		parameter("$isPrefix", types.Bool(), true),
	)

	builder.class("GdImage", semantic.ClassSymbol)
	builder.class("CurlHandle", semantic.ClassSymbol)
	collator := builder.class("Collator", semantic.ClassSymbol)
	for _, name := range []string{
		"NUMERIC_COLLATION",
		"ALTERNATE_HANDLING",
		"ON",
		"SHIFTED",
	} {
		builder.classConstant(collator, name, types.Int())
	}
	builder.method(collator, "__construct", types.Void(),
		parameter("$locale", types.String(), false),
	)
	builder.method(collator, "setAttribute", types.Bool(),
		parameter("$attribute", types.Int(), false),
		parameter("$value", types.Int(), false),
	)
	builder.method(
		collator,
		"getSortKey",
		types.Union(types.String(), types.False()),
		parameter("$string", types.String(), false),
	)
	numberFormatter := builder.class("NumberFormatter", semantic.ClassSymbol)
	builder.classConstant(numberFormatter, "CURRENCY", types.Int())
	builder.classConstant(numberFormatter, "FRACTION_DIGITS", types.Int())
	builder.method(numberFormatter, "__construct", types.Void(),
		parameter("$locale", types.String(), false),
		parameter("$style", types.Int(), false),
		parameter("$pattern", types.Nullable(types.String()), true),
	)
	builder.method(numberFormatter, "setAttribute", types.Bool(),
		parameter("$attribute", types.Int(), false),
		parameter("$value", types.Int(), false),
	)
	builder.method(numberFormatter, "formatCurrency", types.Union(types.String(), types.False()),
		parameter("$amount", types.Float(), false),
		parameter("$currency", types.String(), false),
	)
	intlDateFormatter := builder.class("IntlDateFormatter", semantic.ClassSymbol)
	builder.classConstant(intlDateFormatter, "MEDIUM", types.Int())
	builder.method(intlDateFormatter, "__construct", types.Void(),
		parameter("$locale", types.Nullable(types.String()), false),
		parameter("$dateType", types.Int(), false),
		parameter("$timeType", types.Int(), false),
	)
	builder.method(intlDateFormatter, "format", types.Union(types.String(), types.False()),
		parameter("$datetime", types.Union(types.Named("DateTimeInterface"), types.Int()), false),
	)
	locale := builder.class("Locale", semantic.ClassSymbol)
	builder.staticMethod(locale, "canonicalize", types.Nullable(types.String()),
		parameter("$locale", types.String(), false),
	)
	builder.staticMethod(locale, "getDefault", types.String())
	builder.staticMethod(locale, "parseLocale", types.Array(types.String(), types.String()),
		parameter("$locale", types.String(), false),
	)

	xmlReader := builder.class("XMLReader", semantic.ClassSymbol)
	for _, name := range []string{
		"NONE", "ELEMENT", "ATTRIBUTE", "TEXT", "CDATA", "ENTITY_REF",
		"ENTITY", "PI", "COMMENT", "DOC", "DOC_TYPE", "WHITESPACE",
		"SIGNIFICANT_WHITESPACE", "END_ELEMENT", "END_ENTITY",
		"XML_DECLARATION",
	} {
		builder.classConstant(xmlReader, name, types.Int())
	}
	builder.property(xmlReader, "nodeType", types.Int())
	builder.property(xmlReader, "name", types.String())
	builder.property(xmlReader, "localName", types.String())
	builder.property(xmlReader, "namespaceURI", types.String())
	builder.property(xmlReader, "prefix", types.String())
	builder.property(xmlReader, "value", types.String())
	builder.property(xmlReader, "isEmptyElement", types.Bool())
	builder.property(xmlReader, "hasAttributes", types.Bool())
	builder.method(xmlReader, "open", types.Bool(),
		parameter("$uri", types.String(), false),
		parameter("$encoding", types.Nullable(types.String()), true),
		parameter("$flags", types.Int(), true),
	)
	builder.method(xmlReader, "read", types.Bool())
	builder.method(xmlReader, "close", types.Bool())
	builder.method(xmlReader, "getAttribute", types.Nullable(types.String()),
		parameter("$name", types.String(), false),
	)
	builder.method(xmlReader, "moveToElement", types.Bool())
	builder.method(xmlReader, "moveToFirstAttribute", types.Bool())
	builder.method(xmlReader, "moveToNextAttribute", types.Bool())
	builder.method(xmlReader, "readInnerXml", types.String())

	zipArchive := builder.class("ZipArchive", semantic.ClassSymbol)
	for _, name := range []string{
		"CREATE", "OVERWRITE", "ER_EXISTS", "ER_INCONS", "ER_INVAL",
		"ER_MEMORY", "ER_NOENT", "ER_NOZIP", "ER_OPEN", "ER_READ",
		"ER_SEEK",
	} {
		builder.classConstant(zipArchive, name, types.Int())
	}
	builder.property(zipArchive, "numFiles", types.Int())
	builder.property(zipArchive, "filename", types.String())
	builder.method(zipArchive, "open", types.Union(types.True(), types.Int()),
		parameter("$filename", types.String(), false),
		parameter("$flags", types.Int(), true),
	)
	builder.method(zipArchive, "close", types.Bool())
	builder.method(zipArchive, "addFromString", types.Bool(),
		parameter("$name", types.String(), false),
		parameter("$content", types.String(), false),
		parameter("$flags", types.Int(), true),
	)
	builder.method(zipArchive, "extractTo", types.Bool(),
		parameter("$path", types.String(), false),
		parameter(
			"$files",
			types.Union(types.Array(types.ArrayKey(), types.String()), types.String(), types.Null()),
			true,
		),
	)
	builder.method(zipArchive, "getFromName", types.Union(types.String(), types.False()),
		parameter("$name", types.String(), false),
		parameter("$length", types.Int(), true),
		parameter("$flags", types.Int(), true),
	)
	builder.method(zipArchive, "statIndex", types.Union(
		types.Array(types.String(), types.Mixed()),
		types.False(),
	), parameter("$index", types.Int(), false), parameter("$flags", types.Int(), true))
	builder.method(zipArchive, "statName", types.Union(
		types.Array(types.String(), types.Mixed()),
		types.False(),
	), parameter("$name", types.String(), false), parameter("$flags", types.Int(), true))

	reflectionAttribute := builder.class("ReflectionAttribute", semantic.ClassSymbol)
	builder.setTemplates(
		reflectionAttribute,
		semantic.TemplateParameter{
			Name:    "TAttribute",
			Bound:   types.Object(),
			Default: types.Object(),
		},
	)
	builder.method(reflectionAttribute, "getName", types.String())
	builder.method(reflectionAttribute, "getArguments", types.Array(types.ArrayKey(), types.Mixed()))
	builder.method(reflectionAttribute, "newInstance", types.Template("TAttribute"))
	attributeMethod := func(container semantic.SymbolID) {
		builder.methodWithTemplates(
			container,
			"getAttributes",
			types.List(types.Named(
				"ReflectionAttribute",
				types.Template("TAttribute"),
			)),
			[]semantic.TemplateParameter{{
				Name:    "TAttribute",
				Bound:   types.Object(),
				Default: types.Object(),
			}},
			parameter(
				"$name",
				types.Nullable(types.ClassString(types.Template("TAttribute"))),
				true,
			),
			parameter("$flags", types.Int(), true),
		)
	}
	reflectionType := builder.class("ReflectionType", semantic.ClassSymbol)
	builder.method(reflectionType, "allowsNull", types.Bool())
	builder.method(reflectionType, "__toString", types.String())
	reflectionNamedType := builder.classExtends(
		"ReflectionNamedType",
		semantic.ClassSymbol,
		"ReflectionType",
	)
	builder.method(reflectionNamedType, "getName", types.String())
	builder.method(reflectionNamedType, "isBuiltin", types.Bool())
	for _, name := range []string{"ReflectionUnionType", "ReflectionIntersectionType"} {
		reflectionComposite := builder.classExtends(
			name,
			semantic.ClassSymbol,
			"ReflectionType",
		)
		builder.method(
			reflectionComposite,
			"getTypes",
			types.List(types.Named("ReflectionType")),
		)
	}
	reflectionMethod := builder.class("ReflectionMethod", semantic.ClassSymbol)
	builder.method(reflectionMethod, "__construct", types.Void(),
		parameter(
			"$objectOrMethod",
			types.Union(types.Object(), types.String()),
			false,
		),
		parameter("$method", types.String(), true),
	)
	builder.method(reflectionMethod, "getName", types.String())
	builder.method(reflectionMethod, "isPublic", types.Bool())
	builder.method(reflectionMethod, "isPrivate", types.Bool())
	builder.method(reflectionMethod, "isAbstract", types.Bool())
	builder.method(reflectionMethod, "isStatic", types.Bool())
	builder.method(reflectionMethod, "hasReturnType", types.Bool())
	builder.method(
		reflectionMethod,
		"getDeclaringClass",
		types.Named("ReflectionClass"),
	)
	builder.method(
		reflectionMethod,
		"getParameters",
		types.List(types.Named("ReflectionParameter")),
	)
	builder.method(
		reflectionMethod,
		"getReturnType",
		types.Nullable(types.Named("ReflectionType")),
	)
	builder.method(
		reflectionMethod,
		"getDocComment",
		types.Union(types.String(), types.False()),
	)
	attributeMethod(reflectionMethod)
	builder.method(reflectionMethod, "invoke", types.Mixed(),
		parameter("$object", types.Nullable(types.Object()), false),
		variadic("$args", types.Mixed()),
	)
	for _, name := range []string{
		"IS_STATIC", "IS_PUBLIC", "IS_PROTECTED", "IS_PRIVATE",
		"IS_ABSTRACT", "IS_FINAL",
	} {
		builder.classConstant(reflectionMethod, name, types.Int())
	}
	reflectionParameter := builder.class("ReflectionParameter", semantic.ClassSymbol)
	builder.method(reflectionParameter, "getName", types.String())
	builder.method(
		reflectionParameter,
		"getType",
		types.Nullable(types.Named("ReflectionType")),
	)
	builder.method(reflectionParameter, "isOptional", types.Bool())
	builder.method(reflectionParameter, "allowsNull", types.Bool())
	builder.method(reflectionParameter, "isVariadic", types.Bool())
	builder.method(reflectionParameter, "isDefaultValueAvailable", types.Bool())
	builder.method(reflectionParameter, "getDefaultValue", types.Mixed())
	attributeMethod(reflectionParameter)
	reflectionProperty := builder.class("ReflectionProperty", semantic.ClassSymbol)
	builder.method(reflectionProperty, "__construct", types.Void(),
		parameter(
			"$class",
			types.Union(types.Object(), types.String()),
			false,
		),
		parameter("$property", types.String(), false),
	)
	builder.method(reflectionProperty, "getName", types.String())
	builder.method(reflectionProperty, "isPublic", types.Bool())
	builder.method(reflectionProperty, "isPrivate", types.Bool())
	builder.method(reflectionProperty, "isReadOnly", types.Bool())
	builder.method(
		reflectionProperty,
		"getType",
		types.Nullable(types.Named("ReflectionType")),
	)
	builder.method(reflectionProperty, "getValue", types.Mixed(),
		parameter("$object", types.Nullable(types.Object()), true),
	)
	builder.method(reflectionProperty, "setValue", types.Void(),
		parameter("$objectOrValue", types.Mixed(), false),
		parameter("$value", types.Mixed(), true),
	)
	builder.method(
		reflectionProperty,
		"getDeclaringClass",
		types.Named("ReflectionClass"),
	)
	builder.method(
		reflectionProperty,
		"getDocComment",
		types.Union(types.String(), types.False()),
	)
	attributeMethod(reflectionProperty)
	reflectionClass := builder.class("ReflectionClass", semantic.ClassSymbol)
	builder.property(reflectionClass, "name", types.String())
	builder.setTemplates(
		reflectionClass,
		semantic.TemplateParameter{
			Name:    "TObject",
			Bound:   types.Object(),
			Default: types.Object(),
		},
	)
	builder.method(reflectionClass, "__construct", types.Void(),
		parameter(
			"$objectOrClass",
			types.Union(
				types.Template("TObject"),
				types.ClassString(types.Template("TObject")),
				types.String(),
			),
			false,
		),
	)
	builder.method(reflectionClass, "getName", types.String())
	builder.method(reflectionClass, "getShortName", types.String())
	builder.method(
		reflectionClass,
		"getFileName",
		types.Union(types.String(), types.False()),
	)
	builder.method(
		reflectionClass,
		"getDocComment",
		types.Union(types.String(), types.False()),
	)
	builder.method(reflectionClass, "isAbstract", types.Bool())
	builder.method(reflectionClass, "isFinal", types.Bool())
	builder.method(reflectionClass, "isInterface", types.Bool())
	builder.method(reflectionClass, "isTrait", types.Bool())
	builder.method(reflectionClass, "isInstantiable", types.Bool())
	builder.method(reflectionClass, "hasMethod", types.Bool(),
		parameter("$name", types.String(), false),
	)
	builder.method(reflectionClass, "hasProperty", types.Bool(),
		parameter("$name", types.String(), false),
	)
	builder.method(reflectionClass, "hasConstant", types.Bool(),
		parameter("$name", types.String(), false),
	)
	builder.method(reflectionClass, "implementsInterface", types.Bool(),
		parameter("$interface", types.String(), false),
	)
	builder.method(reflectionClass, "isSubclassOf", types.Bool(),
		parameter("$class", types.String(), false),
	)
	builder.method(reflectionClass, "getNamespaceName", types.String())
	builder.method(
		reflectionClass,
		"getParentClass",
		types.Union(types.Named("ReflectionClass"), types.False()),
	)
	builder.method(
		reflectionClass,
		"getConstructor",
		types.Nullable(types.Named("ReflectionMethod")),
	)
	builder.method(reflectionClass, "getConstant", types.Mixed(),
		parameter("$name", types.String(), false),
	)
	builder.method(reflectionClass, "isInstance", types.Bool(),
		parameter("$object", types.Object(), false),
	)
	attributeMethod(reflectionClass)
	builder.method(reflectionClass, "getMethods", types.List(types.Named("ReflectionMethod")),
		parameter("$filter", types.Nullable(types.Int()), true),
	)
	builder.method(reflectionClass, "getProperties", types.List(types.Named("ReflectionProperty")),
		parameter("$filter", types.Nullable(types.Int()), true),
	)
	builder.method(reflectionClass, "newInstance", types.Template("TObject"),
		variadic("$args", types.Mixed()),
	)
	builder.method(
		reflectionClass,
		"newInstanceArgs",
		types.Template("TObject"),
		parameter(
			"$args",
			types.Array(types.ArrayKey(), types.Mixed()),
			true,
		),
	)
	builder.method(
		reflectionClass,
		"newInstanceWithoutConstructor",
		types.Template("TObject"),
	)
	builder.method(
		reflectionClass,
		"getMethod",
		types.Named("ReflectionMethod"),
		parameter("$name", types.String(), false),
	)
	builder.method(
		reflectionClass,
		"getProperty",
		types.Named("ReflectionProperty"),
		parameter("$name", types.String(), false),
	)
	reflectionObject := builder.classExtends(
		"ReflectionObject",
		semantic.ClassSymbol,
		"ReflectionClass",
	)
	builder.method(reflectionObject, "__construct", types.Void(),
		parameter("$object", types.Object(), false),
	)
	reflectionEnum := builder.classExtends(
		"ReflectionEnum",
		semantic.ClassSymbol,
		"ReflectionClass",
	)
	builder.method(
		reflectionEnum,
		"getBackingType",
		types.Nullable(types.Named("ReflectionNamedType")),
	)
	reflectionFunction := builder.class("ReflectionFunction", semantic.ClassSymbol)
	builder.method(reflectionFunction, "__construct", types.Void(),
		parameter("$function", types.Union(
			types.Named("Closure"),
			types.String(),
		), false),
	)
	builder.method(
		reflectionFunction,
		"getClosureThis",
		types.Nullable(types.Object()),
	)
	attributeMethod(reflectionFunction)

	redis := builder.class("Redis", semantic.ClassSymbol)
	for _, name := range []string{"OPT_PREFIX", "MULTI"} {
		builder.classConstant(redis, name, types.Int())
	}
	builder.method(redis, "setOption", types.Bool(),
		parameter("$option", types.Int(), false),
		parameter("$value", types.Mixed(), false),
	)
	builder.method(redis, "getOption", types.Mixed(),
		parameter("$option", types.Int(), false),
	)
	builder.method(redis, "isConnected", types.Bool())
	redisResult := types.Union(types.Named("Redis"), types.Int(), types.False())
	builder.method(redis, "del", redisResult,
		parameter("$key", types.Union(types.Array(types.Mixed(), types.Mixed()), types.String()), false),
		variadic("$other_keys", types.String()),
	)
	builder.method(redis, "sAdd", redisResult,
		parameter("$key", types.String(), false),
		parameter("$value", types.Mixed(), false),
		variadic("$other_values", types.Mixed()),
	)
	for _, name := range []string{"sPop", "incr", "incrBy", "decr"} {
		builder.method(redis, name, types.Union(types.Int(), types.False()),
			parameter("$key", types.String(), false),
			variadic("$values", types.Mixed()),
		)
	}
	builder.method(redis, "get", types.Mixed(),
		parameter("$key", types.String(), false),
	)
	builder.method(redis, "set", types.Union(types.Named("Redis"), types.String(), types.Bool()),
		parameter("$key", types.String(), false),
		parameter("$value", types.Mixed(), false),
		parameter("$options", types.Mixed(), true),
	)
	builder.method(redis, "keys", types.Union(
		types.Named("Redis"),
		types.Array(types.Mixed(), types.Mixed()),
		types.False(),
	), parameter("$pattern", types.String(), false))
	builder.method(redis, "sMembers", types.Union(
		types.Named("Redis"),
		types.Array(types.Mixed(), types.Mixed()),
		types.False(),
	), parameter("$key", types.String(), false))
	builder.method(redis, "multi", types.Union(types.Named("Redis"), types.Bool()),
		parameter("$value", types.Int(), true),
	)
	builder.method(redis, "exec", types.Union(
		types.Named("Redis"),
		types.Array(types.Mixed(), types.Mixed()),
		types.False(),
	))
	builder.method(redis, "mget", types.Mixed(),
		parameter("$key", types.Mixed(), false),
		variadic("$values", types.Mixed()),
	)
	builder.method(redis, "watch", types.Mixed(), variadic("$args", types.Mixed()))
	builder.classExtends(
		"RedisException",
		semantic.ClassSymbol,
		"RuntimeException",
	)

	imagickPixel := builder.class("ImagickPixel", semantic.ClassSymbol)
	builder.method(imagickPixel, "__construct", types.Void(),
		parameter("$color", types.String(), true),
	)
	imagick := builder.class("Imagick", semantic.ClassSymbol)
	for _, name := range []string{
		"FILTER_LANCZOS", "COMPOSITE_OVER", "INTERLACE_JPEG",
	} {
		builder.classConstant(imagick, name, types.Int())
	}
	builder.method(imagick, "__construct", types.Void())
	builder.method(imagick, "readImageBlob", types.Bool(),
		parameter("$image", types.String(), false),
	)
	builder.method(imagick, "rotateImage", types.Bool(),
		parameter("$background", types.Named("ImagickPixel"), false),
		parameter("$degrees", types.Float(), false),
	)
	builder.method(imagick, "getImageWidth", types.Int())
	builder.method(imagick, "getImageHeight", types.Int())
	builder.method(imagick, "resizeImage", types.Bool(),
		parameter("$columns", types.Int(), false),
		parameter("$rows", types.Int(), false),
		parameter("$filter", types.Int(), false),
		parameter("$blur", types.Float(), false),
	)
	builder.method(imagick, "newImage", types.Bool(),
		parameter("$columns", types.Int(), false),
		parameter("$rows", types.Int(), false),
		parameter("$background", types.Named("ImagickPixel"), false),
	)
	builder.method(imagick, "getImageFormat", types.String())
	builder.method(imagick, "setImageFormat", types.Bool(),
		parameter("$format", types.String(), false),
	)
	builder.method(imagick, "compositeImage", types.Bool(),
		parameter("$compositeImage", types.Named("Imagick"), false),
		parameter("$composite", types.Int(), false),
		parameter("$x", types.Int(), false),
		parameter("$y", types.Int(), false),
	)
	builder.method(imagick, "clear", types.Bool())
	builder.method(imagick, "setImageCompressionQuality", types.Bool(),
		parameter("$quality", types.Int(), false),
	)
	builder.method(imagick, "setInterlaceScheme", types.Bool(),
		parameter("$interlace", types.Int(), false),
	)
	builder.staticMethod(imagick, "queryFormats", types.List(types.String()),
		parameter("$pattern", types.String(), true),
	)
	builder.method(imagick, "getImageBlob", types.String())

	arrayObject := builder.class("ArrayObject", semantic.ClassSymbol)
	builder.addImplements(arrayObject, "IteratorAggregate", "ArrayAccess", "Countable")
	builder.method(arrayObject, "count", types.Int())
	builder.method(arrayObject, "getIterator", types.Named("Iterator"))
	builder.method(arrayObject, "getArrayCopy", types.Array(types.ArrayKey(), types.Mixed()))

	attribute := builder.class("Attribute", semantic.ClassSymbol)
	builder.classConstant(attribute, "TARGET_CLASS", types.Int())
	builder.classConstant(attribute, "TARGET_FUNCTION", types.Int())
	builder.classConstant(attribute, "TARGET_METHOD", types.Int())
	builder.classConstant(attribute, "TARGET_PROPERTY", types.Int())
	builder.classConstant(attribute, "TARGET_CLASS_CONSTANT", types.Int())
	builder.classConstant(attribute, "TARGET_PARAMETER", types.Int())
	builder.classConstant(attribute, "TARGET_ALL", types.Int())
	builder.classConstant(attribute, "IS_REPEATABLE", types.Int())
	builder.method(attribute, "__construct", types.Void(), parameter("$flags", types.Int(), true))
	builder.class("AllowDynamicProperties", semantic.ClassSymbol)
	builder.class("ReturnTypeWillChange", semantic.ClassSymbol)
	builder.class("SensitiveParameter", semantic.ClassSymbol)
	if version.AtLeast(8, 3) {
		builder.class("Override", semantic.ClassSymbol)
	}

	generator := builder.classExtends("Generator", semantic.ClassSymbol, "Iterator")
	builder.method(generator, "getReturn", types.Mixed())
	builder.method(generator, "send", types.Mixed(), parameter("$value", types.Mixed(), false))
	builder.method(generator, "throw", types.Mixed(), parameter("$exception", types.Named("Throwable"), false))
	if version.AtLeast(8, 1) {
		fiber := builder.class("Fiber", semantic.ClassSymbol)
		builder.method(fiber, "__construct", types.Void(), parameter("$callback", types.Callable(nil, types.Mixed()), false))
		builder.method(fiber, "start", types.Mixed(), variadic("$args", types.Mixed()))
		builder.method(fiber, "resume", types.Mixed(), parameter("$value", types.Mixed(), true))
		builder.method(fiber, "isStarted", types.Bool())
		builder.method(fiber, "isTerminated", types.Bool())
		builder.method(fiber, "getReturn", types.Mixed())
	}

	builder.function("count", types.Int(), parameter("$value", types.Union(types.Array(types.Mixed(), types.Mixed()), types.Named("Countable")), false))
	builder.function("strlen", types.Int(), parameter("$string", types.String(), false))
	builder.function("substr", types.String(),
		parameter("$string", types.String(), false),
		parameter("$offset", types.Int(), false),
		parameter("$length", types.Nullable(types.Int()), true),
	)
	builder.function("strpos", types.Union(types.Int(), types.False()),
		parameter("$haystack", types.String(), false),
		parameter("$needle", types.String(), false),
		parameter("$offset", types.Int(), true),
	)
	builder.function("mb_strlen", types.Int(),
		parameter("$string", types.String(), false),
		parameter("$encoding", types.Nullable(types.String()), true),
	)
	builder.function("mb_substr", types.String(),
		parameter("$string", types.String(), false),
		parameter("$start", types.Int(), false),
		parameter("$length", types.Nullable(types.Int()), true),
		parameter("$encoding", types.Nullable(types.String()), true),
	)
	builder.function("mb_strpos", types.Union(types.Int(), types.False()),
		parameter("$haystack", types.String(), false),
		parameter("$needle", types.String(), false),
		parameter("$offset", types.Int(), true),
		parameter("$encoding", types.Nullable(types.String()), true),
	)
	builder.function("sprintf", types.String(), parameter("$format", types.String(), false), variadic("$values", types.Mixed()))
	builder.function("json_encode", types.Union(types.String(), types.False()),
		parameter("$value", types.Mixed(), false),
		parameter("$flags", types.Int(), true),
		parameter("$depth", types.Int(), true),
	)
	builder.function("json_decode", types.Mixed(),
		parameter("$json", types.String(), false),
		parameter("$associative", types.Nullable(types.Bool()), true),
		parameter("$depth", types.Int(), true),
		parameter("$flags", types.Int(), true),
	)
	builder.function("preg_match", types.Union(types.Int(), types.False()),
		parameter("$pattern", types.String(), false),
		parameter("$subject", types.String(), false),
		byReference(
			"$matches",
			types.Array(types.ArrayKey(), types.Mixed()),
			true,
		),
		parameter("$flags", types.Int(), true),
		parameter("$offset", types.Int(), true),
	)
	builder.function("preg_match_all", types.Union(types.Int(), types.False()),
		parameter("$pattern", types.String(), false),
		parameter("$subject", types.String(), false),
		byReference(
			"$matches",
			types.Array(types.ArrayKey(), types.Mixed()),
			true,
		),
		parameter("$flags", types.Int(), true),
		parameter("$offset", types.Int(), true),
	)
	builder.function("parse_str", types.Void(),
		parameter("$string", types.String(), false),
		byReference(
			"$result",
			types.Array(types.ArrayKey(), types.Mixed()),
			true,
		),
	)
	builder.function("get_class", types.ClassString(types.Object()), parameter("$object", types.Object(), true))
	builder.function("class_exists", types.Bool(),
		parameter("$class", types.String(), false),
		parameter("$autoload", types.Bool(), true),
	)
	builder.function("interface_exists", types.Bool(),
		parameter("$interface", types.String(), false),
		parameter("$autoload", types.Bool(), true),
	)
	builder.function("is_a", types.Bool(),
		parameter("$object_or_class", types.Union(types.Object(), types.String()), false),
		parameter("$class", types.String(), false),
		parameter("$allow_string", types.Bool(), true),
	)
	builder.function("in_array", types.Bool(),
		parameter("$needle", types.Mixed(), false),
		parameter("$haystack", types.Array(types.ArrayKey(), types.Mixed()), false),
		parameter("$strict", types.Bool(), true),
	)
	builder.function("array_values", types.List(types.Template("T")), parameter("$array", types.Array(types.ArrayKey(), types.Template("T")), false))
	builder.function("array_keys", types.List(types.Template("TKey")), parameter("$array", types.Array(types.Template("TKey"), types.Mixed()), false))
	builder.function("array_filter", types.Array(types.Template("TKey"), types.Template("TValue")),
		parameter("$array", types.Array(types.Template("TKey"), types.Template("TValue")), false),
		parameter("$callback", types.Nullable(types.Callable(nil, types.Mixed())), true),
		parameter("$mode", types.Int(), true),
	)
	if version.AtLeast(8, 0) {
		builder.function("str_contains", types.Bool(),
			parameter("$haystack", types.String(), false),
			parameter("$needle", types.String(), false),
		)
	}
	if version.AtLeast(8, 3) {
		builder.function("json_validate", types.Bool(), parameter("$json", types.String(), false))
	}
	return &semantic.Document{
		Path:    path,
		Symbols: filterStubSymbols(builder.symbols, extensions),
		CallContracts: func() []semantic.CallContract {
			if extensions == nil {
				return generatedContracts()
			}
			return generatedContractsForExtensions(extensions)
		}(),
	}
}

type stubBuilder struct {
	path    string
	symbols []semantic.Symbol
	index   map[string]int
}

func newStubBuilder(path string, symbols []semantic.Symbol) stubBuilder {
	builder := stubBuilder{
		path:    path,
		symbols: symbols,
		index:   make(map[string]int, len(symbols)),
	}
	for index, symbol := range symbols {
		builder.index[stubSymbolKey(symbol.Kind, symbol.FullyQualified)] = index
	}
	return builder
}

func (b *stubBuilder) class(name string, kind semantic.SymbolKind) semantic.SymbolID {
	id := semantic.NewSymbolID(kind, name, b.path, 0)
	key := stubSymbolKey(kind, name)
	if index, exists := b.index[key]; exists {
		return b.symbols[index].ID
	}
	b.put(semantic.Symbol{
		ID:             id,
		Kind:           kind,
		Name:           name,
		FullyQualified: name,
		Path:           b.path,
		Flags:          semantic.InternalFlag,
	})
	return id
}

func (b *stubBuilder) classExtends(name string, kind semantic.SymbolKind, parent string) semantic.SymbolID {
	id := b.class(name, kind)
	for index := range b.symbols {
		if b.symbols[index].ID == id {
			if kind == semantic.InterfaceSymbol {
				b.symbols[index].SetImplements([]string{parent})
			} else {
				b.symbols[index].SetExtends([]string{parent})
			}
		}
	}
	return id
}

func (b *stubBuilder) addImplements(id semantic.SymbolID, names ...string) {
	for index := range b.symbols {
		if b.symbols[index].ID == id {
			implemented := append([]string(nil), b.symbols[index].Implements()...)
			for _, name := range names {
				if !containsFold(implemented, name) {
					implemented = append(implemented, name)
				}
			}
			b.symbols[index].SetImplements(implemented)
		}
	}
}

func (b *stubBuilder) setTemplates(
	id semantic.SymbolID,
	templates ...semantic.TemplateParameter,
) {
	for index := range b.symbols {
		if b.symbols[index].ID == id {
			b.symbols[index].SetTemplates(
				append([]semantic.TemplateParameter(nil), templates...),
			)
		}
	}
}

func (b *stubBuilder) method(
	container semantic.SymbolID,
	name string,
	result types.Type,
	parameters ...semantic.Parameter,
) {
	owner := b.symbolName(container)
	fqn := owner + "::" + name
	b.put(semantic.Symbol{
		ID:             semantic.NewSymbolID(semantic.MethodSymbol, fqn, b.path, 0),
		Kind:           semantic.MethodSymbol,
		Name:           name,
		FullyQualified: fqn,
		Container:      container,
		Path:           b.path,
		Visibility:     semantic.Public,
		Flags:          semantic.InternalFlag,
		ReturnType:     result,
		NativeType:     result,
		Parameters:     parameters,
	})
}

func (b *stubBuilder) methodWithTemplates(
	container semantic.SymbolID,
	name string,
	result types.Type,
	templates []semantic.TemplateParameter,
	parameters ...semantic.Parameter,
) {
	b.method(container, name, result, parameters...)
	key := stubSymbolKey(semantic.MethodSymbol, b.symbolName(container)+"::"+name)
	if index, exists := b.index[key]; exists {
		b.symbols[index].SetTemplates(
			append([]semantic.TemplateParameter(nil), templates...),
		)
	}
}

func (b *stubBuilder) staticMethod(
	container semantic.SymbolID,
	name string,
	result types.Type,
	parameters ...semantic.Parameter,
) {
	b.method(container, name, result, parameters...)
	key := stubSymbolKey(semantic.MethodSymbol, b.symbolName(container)+"::"+name)
	if index, exists := b.index[key]; exists {
		b.symbols[index].Flags |= semantic.StaticFlag
	}
}

func (b *stubBuilder) staticMethodWithTemplates(
	container semantic.SymbolID,
	name string,
	result types.Type,
	templates []semantic.TemplateParameter,
	parameters ...semantic.Parameter,
) {
	b.methodWithTemplates(container, name, result, templates, parameters...)
	key := stubSymbolKey(semantic.MethodSymbol, b.symbolName(container)+"::"+name)
	if index, exists := b.index[key]; exists {
		b.symbols[index].Flags |= semantic.StaticFlag
	}
}

func (b *stubBuilder) classConstant(
	container semantic.SymbolID,
	name string,
	value types.Type,
) {
	owner := b.symbolName(container)
	fqn := owner + "::" + name
	b.put(semantic.Symbol{
		ID:             semantic.NewSymbolID(semantic.ClassConstantSymbol, fqn, b.path, 0),
		Kind:           semantic.ClassConstantSymbol,
		Name:           name,
		FullyQualified: fqn,
		Container:      container,
		Path:           b.path,
		Visibility:     semantic.Public,
		Flags:          semantic.InternalFlag,
		Type:           value,
		NativeType:     value,
	})
}

func (b *stubBuilder) property(
	container semantic.SymbolID,
	name string,
	value types.Type,
) {
	b.propertyVisibility(container, name, value, semantic.Public)
}

func (b *stubBuilder) propertyVisibility(
	container semantic.SymbolID,
	name string,
	value types.Type,
	visibility semantic.Visibility,
) {
	owner := b.symbolName(container)
	fqn := owner + "::$" + name
	b.put(semantic.Symbol{
		ID:             semantic.NewSymbolID(semantic.PropertySymbol, fqn, b.path, 0),
		Kind:           semantic.PropertySymbol,
		Name:           name,
		FullyQualified: fqn,
		Container:      container,
		Path:           b.path,
		Visibility:     visibility,
		Flags:          semantic.InternalFlag,
		Type:           value,
		NativeType:     value,
	})
}

func (b *stubBuilder) function(
	name string,
	result types.Type,
	parameters ...semantic.Parameter,
) {
	b.put(semantic.Symbol{
		ID:             semantic.NewSymbolID(semantic.FunctionSymbol, name, b.path, 0),
		Kind:           semantic.FunctionSymbol,
		Name:           name,
		FullyQualified: name,
		Path:           b.path,
		Flags:          semantic.InternalFlag,
		ReturnType:     result,
		NativeType:     result,
		Parameters:     parameters,
	})
}

func (b *stubBuilder) put(symbol semantic.Symbol) int {
	key := stubSymbolKey(symbol.Kind, symbol.FullyQualified)
	if index, exists := b.index[key]; exists {
		b.symbols[index] = symbol
		return index
	}
	index := len(b.symbols)
	b.symbols = append(b.symbols, symbol)
	b.index[key] = index
	return index
}

func stubSymbolKey(kind semantic.SymbolKind, fullyQualified string) string {
	return string(rune(kind)) + ":" + strings.ToLower(strings.TrimPrefix(fullyQualified, "\\"))
}

func containsFold(values []string, target string) bool {
	for _, value := range values {
		if strings.EqualFold(value, target) {
			return true
		}
	}
	return false
}

func (b *stubBuilder) symbolName(id semantic.SymbolID) string {
	for _, symbol := range b.symbols {
		if symbol.ID == id {
			return symbol.FullyQualified
		}
	}
	return ""
}

func parameter(name string, value types.Type, optional bool) semantic.Parameter {
	return semantic.Parameter{Name: name, Type: value, NativeType: value, Optional: optional}
}

func variadic(name string, value types.Type) semantic.Parameter {
	return semantic.Parameter{
		Name:       name,
		Type:       value,
		NativeType: value,
		Flags:      semantic.VariadicFlag,
	}
}

func byReference(
	name string,
	value types.Type,
	optional bool,
) semantic.Parameter {
	return semantic.Parameter{
		Name:       name,
		Type:       value,
		NativeType: value,
		Flags:      semantic.ByReferenceFlag,
		Optional:   optional,
	}
}
