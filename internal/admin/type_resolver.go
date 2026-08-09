package admin

import (
	"path/filepath"
	"strings"
)

type vueTypeResolver struct {
	index         *AdminComponentIndexer
	contextPath   string
	seen          map[string]bool
	substitutions map[string]string
	liveFiles     map[string]AdminTypeFile
}

func (r vueTypeResolver) resolve(typeExpression string) (VueTypeShape, error) {
	value := strings.TrimSpace(substituteAdminType(typeExpression, r.substitutions))
	result := VueTypeShape{Type: value}
	if value == "" {
		return result, nil
	}
	if asserted := vueTypeAssertion(value); asserted != "" {
		value = asserted
	}
	value = trimAdminTypeParentheses(value)
	result.Type = value
	if shape, matched, err := r.resolveWrapper(value); matched {
		return shape, err
	}
	if union := splitAdminTypeTopLevel(value, '|'); len(union) > 1 {
		return r.resolveUnion(value, union)
	}
	if intersection := splitAdminTypeTopLevel(value, '&'); len(intersection) > 1 {
		return r.resolveIntersection(value, intersection)
	}
	if shape, matched := r.resolveArray(value); matched {
		return shape, nil
	}
	if shape, matched := r.resolveObject(value); matched {
		return shape, nil
	}
	name, arguments := parseAdminNamedType(value)
	if name == "" {
		result.Complete = adminPrimitiveType(value)
		return result, nil
	}
	if shape, matched, err := r.resolveSpecial(value, name, arguments); matched {
		return shape, err
	}
	return r.resolveDeclaration(value, name, arguments)
}

func (r vueTypeResolver) resolveWrapper(
	value string,
) (VueTypeShape, bool, error) {
	for _, wrapper := range []string{
		"Readonly", "Partial", "Required", "NonNullable", "PropType",
	} {
		inner, matched := adminTypeGenericInner(value, wrapper)
		if !matched {
			continue
		}
		shape, err := r.resolve(inner)
		if wrapper == "Partial" || wrapper == "Required" {
			for memberIndex := range shape.Members {
				shape.Members[memberIndex].Optional = wrapper == "Partial"
			}
		}
		shape.Type = value
		return shape, true, err
	}
	return VueTypeShape{}, false, nil
}

func (r vueTypeResolver) resolveUnion(
	value string,
	branches []string,
) (VueTypeShape, error) {
	result := VueTypeShape{Type: value}
	var shapes []VueTypeShape
	for _, branch := range branches {
		branch = strings.TrimSpace(branch)
		if nullableAdminTypeBranch(branch) {
			continue
		}
		shape, err := r.resolve(branch)
		if err != nil {
			return result, err
		}
		shapes = append(shapes, shape)
	}
	switch len(shapes) {
	case 0:
		result.Complete = true
	case 1:
		shapes[0].Type = value
		return shapes[0], nil
	default:
		result.Members = unionVueTypeMembers(shapes)
		result.Complete = allVueTypeShapesComplete(shapes)
	}
	return result, nil
}

func nullableAdminTypeBranch(value string) bool {
	return value == "null" || value == "undefined" || value == "never"
}

func allVueTypeShapesComplete(shapes []VueTypeShape) bool {
	for _, shape := range shapes {
		if !shape.Complete {
			return false
		}
	}
	return true
}

func (r vueTypeResolver) resolveIntersection(
	value string,
	branches []string,
) (VueTypeShape, error) {
	result := VueTypeShape{Type: value, Complete: true}
	for _, branch := range branches {
		shape, err := r.resolve(branch)
		if err != nil {
			return result, err
		}
		result.Members = mergeTwigVueMembers(result.Members, shape.Members)
		result.Complete = result.Complete && shape.Complete
	}
	return result, nil
}

func (r vueTypeResolver) resolveArray(value string) (VueTypeShape, bool) {
	arrayValue := strings.TrimSpace(strings.TrimPrefix(value, "readonly "))
	if !strings.HasSuffix(arrayValue, "[]") {
		return VueTypeShape{}, false
	}
	shape, _ := adminJavaScriptBuiltinShape(
		value,
		"Array",
		[]string{strings.TrimSpace(strings.TrimSuffix(arrayValue, "[]"))},
	)
	return shape, true
}

func (r vueTypeResolver) resolveObject(value string) (VueTypeShape, bool) {
	// Only a top-level object literal describes the current receiver. Generic
	// arguments such as Array<{ id: string }> contain braces too, but exposing
	// the element fields as array members incorrectly makes `length` unknown.
	if !strings.HasPrefix(value, "{") || balancedBraceEnd(value, 0) != len(value)-1 {
		return VueTypeShape{}, false
	}
	result := VueTypeShape{
		Type:     value,
		Members:  VueTypeMembers(value),
		Complete: !adminObjectTypeHasIndexSignature(value, 0),
	}
	for memberIndex := range result.Members {
		result.Members[memberIndex].DefinitionPath = r.contextPath
	}
	return result, true
}

func (r vueTypeResolver) resolveSpecial(
	value,
	name string,
	arguments []string,
) (VueTypeShape, bool, error) {
	if builtin, matched := adminJavaScriptBuiltinShape(value, name, arguments); matched {
		return builtin, true, nil
	}
	if (name == "Pick" || name == "Omit") && len(arguments) == 2 {
		shape, err := r.resolvePickOrOmit(value, name, arguments)
		return shape, true, err
	}
	if entityName, matched := adminNamedStringGeneric(name, arguments, "Repository"); matched {
		return adminRepositoryShape(value, entityName), true, nil
	}
	if storeName, matched := adminNamedStringGeneric(name, arguments, "AdminStore"); matched {
		shape, err := r.index.resolveAdminStoreType(value, storeName)
		return shape, true, err
	}
	if entityName, collection, matched := adminEntityGeneric(name, arguments); matched {
		if collection {
			return adminEntityCollectionShape(value, entityName), true, nil
		}
		shape, err := r.index.resolveAdminEntityType(
			value,
			entityName,
			r.contextPath,
			r.seen,
			r.liveFiles,
		)
		return shape, true, err
	}
	return VueTypeShape{}, false, nil
}

func (r vueTypeResolver) resolvePickOrOmit(
	value,
	name string,
	arguments []string,
) (VueTypeShape, error) {
	shape, err := r.resolve(arguments[0])
	if err != nil {
		return VueTypeShape{Type: value}, err
	}
	keys, complete := vueStringLiteralUnionValues(
		substituteAdminType(arguments[1], r.substitutions),
	)
	selected := make(map[string]bool, len(keys))
	for _, key := range keys {
		selected[key] = true
	}
	if name == "Pick" && !complete {
		return VueTypeShape{Type: value}, nil
	}
	members := make([]TwigVueMember, 0, len(shape.Members))
	for _, member := range shape.Members {
		include := selected[member.Name]
		if name == "Omit" {
			include = !include
		}
		if include {
			members = append(members, member)
		}
	}
	shape.Type = value
	shape.Members = members
	shape.Complete = shape.Complete && complete
	return shape, nil
}

func (r vueTypeResolver) resolveDeclaration(
	value,
	name string,
	arguments []string,
) (VueTypeShape, error) {
	result := VueTypeShape{Type: value}
	key := filepath.Clean(r.contextPath) + "\x00" + name + "\x00" +
		strings.Join(arguments, "\x00")
	if r.seen[key] {
		return result, nil
	}
	r.seen[key] = true
	defer delete(r.seen, key)
	declaration, declarationContext, found, err :=
		r.index.resolveAdminTypeDeclaration(r.contextPath, name, r.liveFiles)
	if err != nil || !found {
		result.Complete = adminPrimitiveType(value)
		return result, err
	}
	localSubstitutions := declarationSubstitutions(
		declaration,
		arguments,
		r.substitutions,
	)
	result.Members = substituteVueTypeMembers(declaration.Members, localSubstitutions)
	result.Complete = declaration.Interface && !declaration.Open
	child := vueTypeResolver{
		index:         r.index,
		contextPath:   declarationContext,
		seen:          r.seen,
		substitutions: localSubstitutions,
		liveFiles:     r.liveFiles,
	}
	for _, parent := range declaration.Extends {
		shape, parentErr := child.resolve(parent)
		if parentErr != nil {
			return result, parentErr
		}
		result.Members = mergeTwigVueMembers(shape.Members, result.Members)
		result.Complete = result.Complete && shape.Complete
	}
	if declaration.Alias != "" {
		shape, aliasErr := child.resolve(declaration.Alias)
		if aliasErr != nil {
			return result, aliasErr
		}
		result.Members = mergeTwigVueMembers(shape.Members, result.Members)
		result.Complete = shape.Complete
	}
	return result, nil
}

func declarationSubstitutions(
	declaration AdminTypeDeclaration,
	arguments []string,
	substitutions map[string]string,
) map[string]string {
	result := make(map[string]string, len(substitutions)+len(arguments))
	for name, value := range substitutions {
		result[name] = value
	}
	for parameterIndex, parameter := range declaration.Parameters {
		if parameterIndex < len(arguments) {
			result[parameter] = substituteAdminType(
				arguments[parameterIndex],
				substitutions,
			)
		}
	}
	return result
}
