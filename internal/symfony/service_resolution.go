package symfony

import "strings"

// ResolveConfiguredServiceClass resolves a service receiver from the current
// unsaved configuration first, then falls back to the workspace index. An
// explicit class wins over the service ID and aliases are followed with cycle
// protection.
func ResolveConfiguredServiceClass(
	serviceID,
	explicitClass string,
	local map[string]Service,
	index *ServiceIndex,
) (string, bool, error) {
	if className := staticConfiguredServiceClass(explicitClass); className != "" {
		return className, true, nil
	}
	visited := make(map[string]struct{})
	for serviceID != "" {
		if _, duplicate := visited[serviceID]; duplicate {
			return "", false, nil
		}
		visited[serviceID] = struct{}{}
		service, found := local[serviceID]
		if !found {
			break
		}
		if service.AliasTarget != "" {
			serviceID = service.AliasTarget
			continue
		}
		if className := staticConfiguredServiceClass(service.Class); className != "" {
			return className, true, nil
		}
		return "", false, nil
	}
	if index == nil {
		return "", false, nil
	}
	return index.ResolveServiceClassName(serviceID)
}

func staticConfiguredServiceClass(value string) string {
	value = strings.TrimSpace(strings.TrimPrefix(value, "\\"))
	if value == "" || strings.ContainsAny(value, "%${}@ \t") {
		return ""
	}
	return value
}

// ResolveServiceClassName resolves a service ID to its concrete PHP class,
// following aliases and rejecting cyclic or dynamic definitions.
func (idx *ServiceIndex) ResolveServiceClassName(
	serviceID string,
) (string, bool, error) {
	if idx == nil || serviceID == "" {
		return "", false, nil
	}
	visited := make(map[string]struct{})
	for serviceID != "" {
		if _, duplicate := visited[serviceID]; duplicate {
			return "", false, nil
		}
		visited[serviceID] = struct{}{}
		service, found, err := idx.GetServiceByID(serviceID)
		if err != nil || !found {
			return "", false, err
		}
		if service.AliasTarget != "" {
			serviceID = service.AliasTarget
			continue
		}
		className := strings.TrimSpace(strings.TrimPrefix(service.Class, "\\"))
		if staticYAMLServiceClassName(className) {
			return className, true, nil
		}
		return "", false, nil
	}
	return "", false, nil
}
