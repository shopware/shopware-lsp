package doctrine

import (
	"regexp"
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/shopware/shopware-lsp/internal/php"
)

var doctrineAnnotationPattern = regexp.MustCompile(
	`(?i)@(?:\\?Doctrine\\(?:ORM\\Mapping|ODM\\MongoDB\\Mapping\\Annotations)|ORM|ODM)\\?([A-Za-z]+)(?:\s*\(([^)]*)\))?`,
)

var annotationNamedStringPattern = regexp.MustCompile(
	`(?i)([A-Za-z][A-Za-z0-9_]*)\s*=\s*(?:"([^"]*)"|'([^']*)'|([A-Za-z_\\][A-Za-z0-9_\\]*)(?:::class)?)`,
)

var annotationMapEntryPattern = regexp.MustCompile(
	`(?i)(?:"([^"]+)"|'([^']+)')\s*(?:=>|=)\s*(?:"([^"]+)"|'([^']+)'|([A-Za-z_\\][A-Za-z0-9_\\]*)(?:::class)?)`,
)

var annotationConstraintListPattern = regexp.MustCompile(
	`(?i)(fields|columns)\s*=\s*\{([^}]*)\}`,
)

var annotationConstraintValuePattern = regexp.MustCompile(
	`"([^"]*)"|'([^']*)'`,
)

var doctrineConstraintAnnotationPattern = regexp.MustCompile(
	`(?i)@(?:\\?Doctrine\\ORM\\Mapping|ORM)\\?(Index|UniqueConstraint)\s*\(([^)]*)\)`,
)

func parsePHPModels(
	path string,
	root *phpsyntax.Node,
	source string,
) []Model {
	if root == nil {
		return nil
	}
	resolver := php.NewNameResolver(root)
	var result []Model
	for _, class := range phpquery.Classes(root) {
		if phpquery.IsInterface(class) || phpquery.IsTrait(class) {
			continue
		}
		className := normalizeClass(
			resolver.Resolve(phpquery.ClassName(class)),
		)
		if className == "" {
			continue
		}
		model, mapped := phpAttributeModel(path, class, resolver)
		if !mapped {
			model, mapped = phpAnnotationModel(
				path,
				class,
				resolver,
				source,
			)
		} else {
			mergePHPAnnotations(&model, class, resolver, source)
		}
		if !mapped {
			continue
		}
		model.Class = className
		model.Parent = resolvedParent(class, resolver)
		model.File = path
		model.Range = class.RangeTrimmedTrivia()
		model.NameRange = phpClassNameRange(class)
		result = append(result, normalizeModel(model))
	}
	return result
}

func phpAttributeModel(
	path string,
	class *phpsyntax.Node,
	resolver *php.NameResolver,
) (Model, bool) {
	model := Model{
		Kind:   EntityModel,
		Source: PHPAttributeSource,
		File:   path,
	}
	mapped := false
	for _, attribute := range phpquery.Attributes(class) {
		switch doctrineAttributeName(attribute, resolver) {
		case "Entity", "Document":
			mapped = true
			if doctrineAttributeName(attribute, resolver) == "Document" {
				model.Kind = DocumentModel
			}
			if value, rng := phpAttributeClassValue(
				attribute,
				"repositoryClass",
				-1,
				resolver,
			); value != "" {
				model.Repository = value
				model.RepositoryRange = rng
			}
		case "MappedSuperclass":
			mapped = true
			model.Kind = MappedSuperclassModel
		case "Embeddable":
			mapped = true
			model.Kind = EmbeddableModel
		case "Table":
			model.Table, _ = phpAttributeStringValue(
				attribute,
				"name",
				0,
			)
		case "InheritanceType":
			model.InheritanceType, _ = phpAttributeStringValue(
				attribute,
				"value",
				0,
			)
		case "DiscriminatorColumn":
			model.DiscriminatorColumn, _ = phpAttributeStringValue(
				attribute,
				"name",
				0,
			)
			model.DiscriminatorType, _ = phpAttributeStringValue(
				attribute,
				"type",
				1,
			)
		case "DiscriminatorMap":
			model.DiscriminatorMap = append(
				model.DiscriminatorMap,
				phpAttributeDiscriminatorMap(
					path,
					attribute,
					resolver,
				)...,
			)
		case "Index", "UniqueConstraint":
			model.TableConstraints = append(
				model.TableConstraints,
				phpAttributeTableConstraint(
					path,
					attribute,
					doctrineAttributeName(attribute, resolver),
				),
			)
		}
	}
	if !mapped {
		return Model{}, false
	}
	model.Fields = phpAttributeFields(path, class, resolver)
	model.Callbacks = phpAttributeCallbacks(path, class, resolver)
	return model, true
}

func phpAttributeTableConstraint(
	path string,
	attribute *phpsyntax.Node,
	name string,
) TableConstraint {
	constraint := TableConstraint{
		Kind: IndexConstraint,
		File: path,
	}
	if strings.EqualFold(name, "UniqueConstraint") {
		constraint.Kind = UniqueConstraint
	}
	constraint.Name, constraint.NameRange =
		phpAttributeStringValue(attribute, "name", 0)
	constraint.Fields = phpAttributeStringReferences(
		attribute,
		"fields",
	)
	constraint.Columns = phpAttributeStringReferences(
		attribute,
		"columns",
	)
	return constraint
}

func phpAttributeStringReferences(
	attribute *phpsyntax.Node,
	name string,
) []TableConstraintReference {
	value, found := phpAttributeValue(attribute, name, -1)
	if !found {
		return nil
	}
	array := phpquery.ArrayAt(value)
	if array == nil ||
		array.RangeTrimmedTrivia() != value.RangeTrimmedTrivia() {
		return nil
	}
	var result []TableConstraintReference
	for _, item := range phpquery.ArrayItems(array) {
		node := phpquery.ArrayItemValue(item)
		stringNode := phpquery.StringAt(node)
		if stringNode == nil ||
			stringNode.RangeTrimmedTrivia() !=
				node.RangeTrimmedTrivia() {
			continue
		}
		result = append(result, TableConstraintReference{
			Name:  phpquery.StringValue(stringNode),
			Range: phpquery.StringContentRange(stringNode),
		})
	}
	return result
}

func phpAttributeFields(
	path string,
	class *phpsyntax.Node,
	resolver *php.NameResolver,
) []Field {
	className := normalizeClass(
		resolver.Resolve(phpquery.ClassName(class)),
	)
	var result []Field
	for _, property := range phpquery.Properties(class) {
		attributes := phpquery.Attributes(property)
		if len(attributes) == 0 {
			continue
		}
		for _, variable := range phpquery.PropertyVariables(property) {
			field := Field{
				Name:    strings.TrimPrefix(phpquery.VariableName(variable), "$"),
				PHPType: resolvedPropertyType(property, resolver),
				Class:   className,
				File:    path,
				Range:   variable.RangeTrimmedTrivia(),
			}
			mapped := false
			for _, attribute := range attributes {
				name := doctrineAttributeName(attribute, resolver)
				switch name {
				case "Id":
					mapped = true
				case "Column", "Field":
					mapped = true
					field.Column, _ = phpAttributeStringValue(
						attribute,
						"name",
						0,
					)
					field.Type, field.TypeRange = phpAttributeStringValue(
						attribute,
						"type",
						1,
					)
					field.EnumType, field.EnumTypeRange = phpAttributeClassValue(
						attribute,
						"enumType",
						-1,
						resolver,
					)
				case "OneToOne", "OneToMany", "ManyToOne", "ManyToMany",
					"ReferenceOne", "ReferenceMany":
					mapped = true
					field.RelationType = name
					field.Relation, field.RelationRange =
						phpAttributeClassValueNames(
							attribute,
							0,
							resolver,
							"targetEntity",
							"targetDocument",
						)
					if field.Relation == "" {
						field.Relation = propertyClassType(
							property,
							resolver,
						)
					}
				case "Embedded", "EmbedOne", "EmbedMany":
					mapped = true
					field.RelationType = name
					field.EmbeddedClass, field.EmbeddedRange =
						phpAttributeClassValueNames(
							attribute,
							0,
							resolver,
							"class",
							"targetDocument",
						)
					if field.EmbeddedClass == "" {
						field.EmbeddedClass = propertyClassType(
							property,
							resolver,
						)
					}
					field.ColumnPrefix = field.Name + "_"
					if value, found := phpAttributeValue(
						attribute,
						"columnPrefix",
						1,
					); found {
						text := strings.TrimSpace(value.Text())
						if strings.EqualFold(text, "false") {
							field.ColumnPrefix = ""
						} else if stringValue := phpquery.StringValue(value); stringValue != "" {
							field.ColumnPrefix = stringValue
						}
					}
				}
			}
			if mapped && field.Name != "" {
				result = append(result, field)
			}
		}
	}
	return result
}

func phpAttributeCallbacks(
	path string,
	class *phpsyntax.Node,
	resolver *php.NameResolver,
) []LifecycleCallback {
	className := normalizeClass(
		resolver.Resolve(phpquery.ClassName(class)),
	)
	events := map[string]string{
		"PrePersist":  "prePersist",
		"PostPersist": "postPersist",
		"PreUpdate":   "preUpdate",
		"PostUpdate":  "postUpdate",
		"PreRemove":   "preRemove",
		"PostRemove":  "postRemove",
		"PostLoad":    "postLoad",
		"PreFlush":    "preFlush",
	}
	var result []LifecycleCallback
	for _, method := range phpquery.Methods(class) {
		for _, attribute := range phpquery.Attributes(method) {
			event, exists := events[doctrineAttributeName(attribute, resolver)]
			if !exists {
				continue
			}
			result = append(result, LifecycleCallback{
				Event:  event,
				Method: phpquery.MethodName(method),
				Class:  className,
				File:   path,
				Range:  method.RangeTrimmedTrivia(),
			})
		}
	}
	return result
}

func phpAnnotationModel(
	path string,
	class *phpsyntax.Node,
	resolver *php.NameResolver,
	source string,
) (Model, bool) {
	doc, docRange := leadingDocblock(
		source,
		class.RangeTrimmedTrivia().Start,
	)
	annotations := parseDoctrineAnnotationsAt(doc, docRange.Start)
	model := Model{
		Kind:   EntityModel,
		Source: PHPAnnotationSource,
		File:   path,
	}
	mapped := false
	for _, annotation := range annotations {
		switch annotation.name {
		case "entity", "document":
			mapped = true
			if annotation.name == "document" {
				model.Kind = DocumentModel
			}
			model.Repository = resolveAnnotationClass(
				annotation.arguments["repositoryclass"],
				resolver,
			)
			model.RepositoryRange = annotation.ranges["repositoryclass"]
		case "mappedsuperclass":
			mapped = true
			model.Kind = MappedSuperclassModel
		case "embeddable":
			mapped = true
			model.Kind = EmbeddableModel
		case "table":
			model.Table = annotation.arguments["name"]
		case "inheritancetype":
			model.InheritanceType = annotation.arguments["value"]
			if model.InheritanceType == "" {
				model.InheritanceType = annotation.positional
			}
		case "discriminatorcolumn":
			model.DiscriminatorColumn = annotation.arguments["name"]
			model.DiscriminatorType = annotation.arguments["type"]
		case "discriminatormap":
			model.DiscriminatorMap = append(
				model.DiscriminatorMap,
				phpAnnotationDiscriminatorMap(
					path,
					annotation,
					resolver,
				)...,
			)
		}
	}
	for _, annotation := range parseDoctrineConstraintAnnotationsAt(
		doc,
		docRange.Start,
	) {
		model.TableConstraints = append(
			model.TableConstraints,
			phpAnnotationTableConstraint(path, annotation),
		)
	}
	if !mapped {
		return Model{}, false
	}
	model.Fields = phpAnnotationFields(path, class, resolver, source)
	model.Callbacks = phpAnnotationCallbacks(path, class, resolver, source)
	return model, true
}

func mergePHPAnnotations(
	model *Model,
	class *phpsyntax.Node,
	resolver *php.NameResolver,
	source string,
) {
	if model == nil {
		return
	}
	annotated, found := phpAnnotationModel(
		model.File,
		class,
		resolver,
		source,
	)
	if !found {
		return
	}
	if model.Repository == "" {
		model.Repository = annotated.Repository
	}
	if model.Table == "" {
		model.Table = annotated.Table
	}
	if model.InheritanceType == "" {
		model.InheritanceType = annotated.InheritanceType
	}
	if model.DiscriminatorColumn == "" {
		model.DiscriminatorColumn = annotated.DiscriminatorColumn
	}
	if model.DiscriminatorType == "" {
		model.DiscriminatorType = annotated.DiscriminatorType
	}
	model.Fields = append(model.Fields, annotated.Fields...)
	model.Callbacks = append(model.Callbacks, annotated.Callbacks...)
	model.DiscriminatorMap = append(
		model.DiscriminatorMap,
		annotated.DiscriminatorMap...,
	)
	model.TableConstraints = append(
		model.TableConstraints,
		annotated.TableConstraints...,
	)
}

func phpAnnotationTableConstraint(
	path string,
	annotation doctrineAnnotation,
) TableConstraint {
	constraint := TableConstraint{
		Name:      annotation.arguments["name"],
		Kind:      IndexConstraint,
		File:      path,
		NameRange: annotation.ranges["name"],
	}
	if annotation.name == "uniqueconstraint" {
		constraint.Kind = UniqueConstraint
	}
	for _, match := range annotationConstraintListPattern.
		FindAllStringSubmatchIndex(annotation.rawArguments, -1) {
		if len(match) < 6 || match[2] < 0 || match[3] < 0 ||
			match[4] < 0 || match[5] < 0 {
			continue
		}
		kind := strings.ToLower(
			annotation.rawArguments[match[2]:match[3]],
		)
		body := annotation.rawArguments[match[4]:match[5]]
		var references []TableConstraintReference
		for _, valueMatch := range annotationConstraintValuePattern.
			FindAllStringSubmatchIndex(body, -1) {
			start, end := firstAnnotationCapture(
				valueMatch,
				2,
				4,
			)
			if start < 0 {
				continue
			}
			references = append(
				references,
				TableConstraintReference{
					Name: body[start:end],
					Range: cst.TextRange{
						Start: annotation.argumentsBase +
							uint32(match[4]+start),
						End: annotation.argumentsBase +
							uint32(match[4]+end),
					},
				},
			)
		}
		if len(references) == 0 {
			position := annotation.argumentsBase +
				uint32(match[4])
			references = append(
				references,
				TableConstraintReference{
					Range: cst.TextRange{
						Start: position,
						End:   position,
					},
				},
			)
		}
		if kind == "fields" {
			constraint.Fields = append(
				constraint.Fields,
				references...,
			)
		} else {
			constraint.Columns = append(
				constraint.Columns,
				references...,
			)
		}
	}
	return constraint
}

func phpAnnotationFields(
	path string,
	class *phpsyntax.Node,
	resolver *php.NameResolver,
	source string,
) []Field {
	className := normalizeClass(
		resolver.Resolve(phpquery.ClassName(class)),
	)
	var result []Field
	for _, property := range phpquery.Properties(class) {
		doc, docRange := leadingDocblock(
			source,
			property.RangeTrimmedTrivia().Start,
		)
		annotations := parseDoctrineAnnotationsAt(doc, docRange.Start)
		if len(annotations) == 0 {
			continue
		}
		for _, variable := range phpquery.PropertyVariables(property) {
			field := Field{
				Name:    strings.TrimPrefix(phpquery.VariableName(variable), "$"),
				PHPType: resolvedPropertyType(property, resolver),
				Class:   className,
				File:    path,
				Range:   variable.RangeTrimmedTrivia(),
			}
			mapped := false
			for _, annotation := range annotations {
				switch annotation.name {
				case "id":
					mapped = true
				case "column", "field":
					mapped = true
					field.Column = annotation.arguments["name"]
					field.Type = annotation.arguments["type"]
					field.TypeRange = annotation.ranges["type"]
					field.EnumType = resolveAnnotationClass(
						annotation.arguments["enumtype"],
						resolver,
					)
					field.EnumTypeRange = annotation.ranges["enumtype"]
				case "onetoone", "onetomany", "manytoone", "manytomany",
					"referenceone", "referencemany":
					mapped = true
					field.RelationType = canonicalAnnotationName(annotation.name)
					targetName := "targetentity"
					if annotation.arguments[targetName] == "" {
						targetName = "targetdocument"
					}
					field.Relation = resolveAnnotationClass(
						annotation.arguments[targetName],
						resolver,
					)
					field.RelationRange = annotation.ranges[targetName]
					if field.Relation == "" {
						field.Relation = propertyClassType(property, resolver)
					}
				case "embedded", "embedone", "embedmany":
					mapped = true
					field.RelationType = canonicalAnnotationName(annotation.name)
					targetName := "class"
					if annotation.arguments[targetName] == "" {
						targetName = "targetdocument"
					}
					field.EmbeddedClass = resolveAnnotationClass(
						annotation.arguments[targetName],
						resolver,
					)
					field.EmbeddedRange = annotation.ranges[targetName]
					if field.EmbeddedClass == "" {
						field.EmbeddedClass = propertyClassType(property, resolver)
					}
					field.ColumnPrefix = field.Name + "_"
					if prefix, exists := annotation.arguments["columnprefix"]; exists {
						if strings.EqualFold(prefix, "false") {
							field.ColumnPrefix = ""
						} else {
							field.ColumnPrefix = prefix
						}
					}
				}
			}
			if mapped && field.Name != "" {
				result = append(result, field)
			}
		}
	}
	return result
}

func phpAnnotationCallbacks(
	path string,
	class *phpsyntax.Node,
	resolver *php.NameResolver,
	source string,
) []LifecycleCallback {
	events := map[string]string{
		"prepersist":  "prePersist",
		"postpersist": "postPersist",
		"preupdate":   "preUpdate",
		"postupdate":  "postUpdate",
		"preremove":   "preRemove",
		"postremove":  "postRemove",
		"postload":    "postLoad",
		"preflush":    "preFlush",
	}
	className := normalizeClass(
		resolver.Resolve(phpquery.ClassName(class)),
	)
	var result []LifecycleCallback
	for _, method := range phpquery.Methods(class) {
		doc, docRange := leadingDocblock(
			source,
			method.RangeTrimmedTrivia().Start,
		)
		for _, annotation := range parseDoctrineAnnotationsAt(
			doc,
			docRange.Start,
		) {
			event, exists := events[annotation.name]
			if !exists {
				continue
			}
			result = append(result, LifecycleCallback{
				Event:  event,
				Method: phpquery.MethodName(method),
				Class:  className,
				File:   path,
				Range:  method.RangeTrimmedTrivia(),
			})
		}
	}
	return result
}

type doctrineAnnotation struct {
	name          string
	arguments     map[string]string
	ranges        map[string]cst.TextRange
	rawArguments  string
	argumentsBase uint32
	positional    string
}

func parseDoctrineAnnotationsAt(
	doc string,
	base uint32,
) []doctrineAnnotation {
	var result []doctrineAnnotation
	for _, match := range doctrineAnnotationPattern.FindAllStringSubmatchIndex(
		doc,
		-1,
	) {
		if len(match) < 6 || match[2] < 0 || match[3] < 0 {
			continue
		}
		annotation := doctrineAnnotation{
			name:      strings.ToLower(doc[match[2]:match[3]]),
			arguments: make(map[string]string),
			ranges:    make(map[string]cst.TextRange),
		}
		if match[4] >= 0 && match[5] >= 0 {
			arguments := doc[match[4]:match[5]]
			annotation.rawArguments = arguments
			annotation.argumentsBase = base + uint32(match[4])
			annotation.positional = strings.Trim(
				strings.TrimSpace(arguments),
				`"'`,
			)
			for _, argument := range annotationNamedStringPattern.FindAllStringSubmatchIndex(
				arguments,
				-1,
			) {
				if len(argument) < 10 ||
					argument[2] < 0 ||
					argument[3] < 0 {
					continue
				}
				name := strings.ToLower(
					arguments[argument[2]:argument[3]],
				)
				valueStart, valueEnd := -1, -1
				for group := 4; group+1 < len(argument); group += 2 {
					if argument[group] >= 0 && argument[group+1] >= 0 {
						valueStart = argument[group]
						valueEnd = argument[group+1]
						break
					}
				}
				if valueStart < 0 || valueEnd < 0 {
					continue
				}
				annotation.arguments[name] = arguments[valueStart:valueEnd]
				annotation.ranges[name] = cst.TextRange{
					Start: base + uint32(match[4]+valueStart),
					End:   base + uint32(match[4]+valueEnd),
				}
			}
		}
		result = append(result, annotation)
	}
	return result
}

func parseDoctrineConstraintAnnotationsAt(
	doc string,
	base uint32,
) []doctrineAnnotation {
	var result []doctrineAnnotation
	for _, match := range doctrineConstraintAnnotationPattern.
		FindAllStringSubmatchIndex(doc, -1) {
		if len(match) < 6 || match[2] < 0 ||
			match[3] < 0 || match[4] < 0 ||
			match[5] < 0 {
			continue
		}
		arguments := doc[match[4]:match[5]]
		annotation := doctrineAnnotation{
			name:          strings.ToLower(doc[match[2]:match[3]]),
			arguments:     make(map[string]string),
			ranges:        make(map[string]cst.TextRange),
			rawArguments:  arguments,
			argumentsBase: base + uint32(match[4]),
		}
		for _, argument := range annotationNamedStringPattern.
			FindAllStringSubmatchIndex(arguments, -1) {
			if len(argument) < 10 ||
				argument[2] < 0 ||
				argument[3] < 0 {
				continue
			}
			name := strings.ToLower(
				arguments[argument[2]:argument[3]],
			)
			valueStart, valueEnd := -1, -1
			for group := 4; group+1 < len(argument); group += 2 {
				if argument[group] >= 0 &&
					argument[group+1] >= 0 {
					valueStart = argument[group]
					valueEnd = argument[group+1]
					break
				}
			}
			if valueStart < 0 {
				continue
			}
			annotation.arguments[name] =
				arguments[valueStart:valueEnd]
			annotation.ranges[name] = cst.TextRange{
				Start: annotation.argumentsBase +
					uint32(valueStart),
				End: annotation.argumentsBase +
					uint32(valueEnd),
			}
		}
		result = append(result, annotation)
	}
	return result
}

func doctrineAttributeName(
	attribute *phpsyntax.Node,
	resolver *php.NameResolver,
) string {
	raw := strings.TrimPrefix(phpquery.AttributeName(attribute), `\`)
	resolved := normalizeClass(resolver.Resolve(raw))
	lower := strings.ToLower(resolved)
	for _, prefix := range []string{
		"doctrine\\orm\\mapping\\",
		"doctrine\\odm\\mongodb\\mapping\\annotations\\",
		"doctrine\\odm\\mongodb\\mapping\\attributes\\",
	} {
		if strings.HasPrefix(lower, prefix) {
			return resolved[strings.LastIndex(resolved, `\`)+1:]
		}
	}
	return ""
}

func phpAttributeDiscriminatorMap(
	path string,
	attribute *phpsyntax.Node,
	resolver *php.NameResolver,
) []DiscriminatorMapping {
	value, found := phpAttributeValue(attribute, "value", 0)
	if !found || value == nil || value.Kind() != phpsyntax.PhpArray {
		return nil
	}
	var result []DiscriminatorMapping
	for _, item := range phpquery.ArrayItems(value) {
		key := phpquery.ArrayItemKey(item)
		classValue := phpquery.ArrayItemValue(item)
		discriminator := phpquery.StringValue(key)
		class, classRange := phpClassExpressionValue(
			classValue,
			resolver,
		)
		if discriminator == "" && class == "" {
			continue
		}
		result = append(result, DiscriminatorMapping{
			Value:      discriminator,
			Class:      class,
			File:       path,
			ValueRange: phpAttributeExpressionRange(key),
			ClassRange: classRange,
		})
	}
	return result
}

func phpClassExpressionValue(
	value *phpsyntax.Node,
	resolver *php.NameResolver,
) (string, cst.TextRange) {
	if value == nil {
		return "", cst.TextRange{}
	}
	class := phpquery.ClassConstantName(value)
	if class != "" {
		return normalizeClass(resolver.Resolve(class)),
			phpAttributeExpressionRange(value)
	}
	class = phpquery.StringValue(value)
	if class == "" {
		return "", value.RangeTrimmedTrivia()
	}
	return resolveAnnotationClass(class, resolver),
		phpAttributeExpressionRange(value)
}

func phpAnnotationDiscriminatorMap(
	path string,
	annotation doctrineAnnotation,
	resolver *php.NameResolver,
) []DiscriminatorMapping {
	if annotation.rawArguments == "" {
		return nil
	}
	var result []DiscriminatorMapping
	for _, match := range annotationMapEntryPattern.FindAllStringSubmatchIndex(
		annotation.rawArguments,
		-1,
	) {
		keyStart, keyEnd := firstAnnotationCapture(match, 2, 4)
		classStart, classEnd := firstAnnotationCapture(match, 6, 8, 10)
		if keyStart < 0 || classStart < 0 {
			continue
		}
		value := annotation.rawArguments[keyStart:keyEnd]
		class := resolveAnnotationClass(
			annotation.rawArguments[classStart:classEnd],
			resolver,
		)
		result = append(result, DiscriminatorMapping{
			Value: value,
			Class: class,
			File:  path,
			ValueRange: cst.TextRange{
				Start: annotation.argumentsBase + uint32(keyStart),
				End:   annotation.argumentsBase + uint32(keyEnd),
			},
			ClassRange: cst.TextRange{
				Start: annotation.argumentsBase + uint32(classStart),
				End:   annotation.argumentsBase + uint32(classEnd),
			},
		})
	}
	return result
}

func firstAnnotationCapture(
	match []int,
	groups ...int,
) (int, int) {
	for _, group := range groups {
		if group+1 < len(match) &&
			match[group] >= 0 && match[group+1] >= 0 {
			return match[group], match[group+1]
		}
	}
	return -1, -1
}

func phpAttributeValue(
	attribute *phpsyntax.Node,
	name string,
	position int,
) (*phpsyntax.Node, bool) {
	positional := 0
	for _, argument := range phpquery.Arguments(attribute) {
		argumentName := phpquery.ArgumentName(argument)
		expression := phpArgumentExpression(argument)
		if strings.EqualFold(argumentName, name) {
			return expression, expression != nil
		}
		if argumentName == "" {
			if positional == position {
				return expression, expression != nil
			}
			positional++
		}
	}
	return nil, false
}

func phpArgumentExpression(argument *phpsyntax.Node) *phpsyntax.Node {
	if argument == nil {
		return nil
	}
	for child := range argument.ChildNodes() {
		if argument.Kind() == phpsyntax.PhpNamedArgument &&
			child.Kind() == phpsyntax.PhpName {
			continue
		}
		return child
	}
	return nil
}

func phpAttributeStringValue(
	attribute *phpsyntax.Node,
	name string,
	position int,
) (string, cst.TextRange) {
	value, found := phpAttributeValue(attribute, name, position)
	if !found {
		return "", cst.TextRange{}
	}
	return phpquery.StringValue(value), phpAttributeExpressionRange(value)
}

func phpAttributeClassValue(
	attribute *phpsyntax.Node,
	name string,
	position int,
	resolver *php.NameResolver,
) (string, cst.TextRange) {
	value, found := phpAttributeValue(attribute, name, position)
	if !found {
		return "", cst.TextRange{}
	}
	className := phpquery.ClassConstantName(value)
	if className != "" {
		return normalizeClass(resolver.Resolve(className)),
			phpAttributeExpressionRange(value)
	}
	className = phpquery.StringValue(value)
	if className == "" {
		return "", cst.TextRange{}
	}
	if strings.Contains(className, `\`) {
		return normalizeClass(className), value.RangeTrimmedTrivia()
	}
	return normalizeClass(resolver.Resolve(className)),
		phpAttributeExpressionRange(value)
}

func phpAttributeClassValueNames(
	attribute *phpsyntax.Node,
	position int,
	resolver *php.NameResolver,
	names ...string,
) (string, cst.TextRange) {
	for _, name := range names {
		if value, found := phpAttributeValue(attribute, name, -1); found {
			return phpClassExpressionValue(value, resolver)
		}
	}
	if position >= 0 {
		value, found := phpAttributeValue(attribute, "", position)
		if found {
			return phpClassExpressionValue(value, resolver)
		}
	}
	return "", cst.TextRange{}
}

func phpAttributeExpressionRange(value *phpsyntax.Node) cst.TextRange {
	if value == nil {
		return cst.TextRange{}
	}
	rng := value.RangeTrimmedTrivia()
	text := strings.TrimSpace(value.Text())
	if len(text) >= 2 &&
		(text[0] == '\'' || text[0] == '"') &&
		text[len(text)-1] == text[0] {
		rng.Start++
		rng.End--
		return rng
	}
	if position := strings.Index(strings.ToLower(text), "::class"); position >= 0 {
		rng.End = rng.Start + uint32(position)
	}
	return rng
}

func resolvedParent(
	class *phpsyntax.Node,
	resolver *php.NameResolver,
) string {
	parents := phpquery.ClassExtends(class)
	if len(parents) == 0 {
		return ""
	}
	return normalizeClass(resolver.Resolve(parents[0]))
}

func resolvedPropertyType(
	property *phpsyntax.Node,
	resolver *php.NameResolver,
) string {
	value := strings.TrimSpace(phpquery.PropertyType(property))
	if value == "" {
		return ""
	}
	nullable := strings.HasPrefix(value, "?")
	if nullable {
		value = strings.TrimPrefix(value, "?")
	}
	resolved, _ := resolvePHPPropertyTypeExpression(value, resolver)
	if nullable && resolved != "" &&
		!phpTypeExpressionContains(resolved, "null") {
		resolved += "|null"
	}
	return resolved
}

func propertyClassType(
	property *phpsyntax.Node,
	resolver *php.NameResolver,
) string {
	value := strings.TrimSpace(phpquery.PropertyType(property))
	_, classes := resolvePHPPropertyTypeExpression(
		strings.TrimPrefix(value, "?"),
		resolver,
	)
	if len(classes) != 0 {
		return classes[0]
	}
	return ""
}

func resolvePHPPropertyTypeExpression(
	value string,
	resolver *php.NameResolver,
) (string, []string) {
	var result strings.Builder
	var classes []string
	for position := 0; position < len(value); {
		if !isPHPPropertyTypeNameStart(value[position]) {
			if value[position] != ' ' && value[position] != '\t' &&
				value[position] != '\r' && value[position] != '\n' {
				result.WriteByte(value[position])
			}
			position++
			continue
		}
		end := position + 1
		for end < len(value) &&
			isPHPPropertyTypeNamePart(value[end]) {
			end++
		}
		name := value[position:end]
		if isBuiltinPHPType(name) {
			result.WriteString(strings.ToLower(name))
		} else {
			resolved := normalizeClass(resolver.Resolve(name))
			result.WriteString(resolved)
			classes = appendUniqueClass(classes, resolved)
		}
		position = end
	}
	return result.String(), classes
}

func isPHPPropertyTypeNameStart(value byte) bool {
	return value == '\\' || value == '_' ||
		value >= 'a' && value <= 'z' ||
		value >= 'A' && value <= 'Z' ||
		value >= 0x80
}

func isPHPPropertyTypeNamePart(value byte) bool {
	return isPHPPropertyTypeNameStart(value) ||
		value >= '0' && value <= '9'
}

func phpTypeExpressionContains(expression, expected string) bool {
	for position := 0; position < len(expression); {
		if !isPHPPropertyTypeNameStart(expression[position]) {
			position++
			continue
		}
		end := position + 1
		for end < len(expression) &&
			isPHPPropertyTypeNamePart(expression[end]) {
			end++
		}
		if strings.EqualFold(expression[position:end], expected) {
			return true
		}
		position = end
	}
	return false
}

func isBuiltinPHPType(value string) bool {
	switch strings.ToLower(value) {
	case "null", "bool", "boolean", "true", "false", "int", "integer",
		"float", "double", "string", "array", "iterable", "callable",
		"object", "mixed", "resource", "void", "never", "self",
		"static", "parent":
		return true
	default:
		return false
	}
}

func phpClassNameRange(class *phpsyntax.Node) cst.TextRange {
	if class == nil {
		return cst.TextRange{}
	}
	name := phpquery.ClassName(class)
	for _, candidate := range phpquery.Nodes(class, phpsyntax.PhpName) {
		if strings.TrimSpace(candidate.Text()) == name {
			return candidate.RangeTrimmedTrivia()
		}
	}
	return class.RangeTrimmedTrivia()
}

func leadingDocblock(source string, offset uint32) (string, cst.TextRange) {
	if int(offset) > len(source) {
		offset = uint32(len(source))
	}
	prefixStart := 0
	if offset > 8192 {
		prefixStart = int(offset) - 8192
	}
	prefix := source[prefixStart:int(offset)]
	end := strings.LastIndex(prefix, "*/")
	if end < 0 {
		return "", cst.TextRange{}
	}
	start := strings.LastIndex(prefix[:end], "/**")
	if start < 0 {
		return "", cst.TextRange{}
	}
	between := prefix[end+2:]
	if strings.TrimSpace(between) != "" {
		return "", cst.TextRange{}
	}
	end += 2
	return prefix[start:end], cst.TextRange{
		Start: uint32(prefixStart + start),
		End:   uint32(prefixStart + end),
	}
}

func resolveAnnotationClass(
	value string,
	resolver *php.NameResolver,
) string {
	value = strings.TrimSpace(strings.TrimSuffix(value, "::class"))
	if value == "" {
		return ""
	}
	if strings.Contains(value, `\`) {
		return normalizeClass(value)
	}
	return normalizeClass(resolver.Resolve(value))
}

func canonicalAnnotationName(value string) string {
	for _, name := range []string{
		"OneToOne", "OneToMany", "ManyToOne", "ManyToMany",
		"ReferenceOne", "ReferenceMany", "Embedded", "EmbedOne", "EmbedMany",
	} {
		if strings.EqualFold(value, name) {
			return name
		}
	}
	return value
}
