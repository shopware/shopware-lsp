package doctrine

import (
	"sort"
	"strings"
)

// NamespaceAliasProvider supplies the legacy namespace maps emitted by a
// compiled Symfony container. The interface deliberately uses plain Go maps
// so the Doctrine package does not depend on the service index.
type NamespaceAliasProvider interface {
	GetDoctrineNamespaceAliasesState() (map[string][]string, uint64)
}

// ModelAlias maps a Doctrine shortcut to its canonical mapped class.
type ModelAlias struct {
	Name      string
	Class     string
	Namespace string
	Weak      bool
}

func (idx *Index) SetNamespaceAliasProvider(provider NamespaceAliasProvider) {
	if idx == nil {
		return
	}
	idx.namespaceProviderMu.Lock()
	idx.namespaceProvider = provider
	idx.namespaceProviderMu.Unlock()
	idx.aliasCacheMu.Lock()
	idx.aliasCacheSet = false
	idx.cachedModelAliases = nil
	idx.cachedAliasTargets = nil
	idx.aliasCacheMu.Unlock()
}

// ModelAliases returns configured compiled-container shortcuts together with
// convention-based bundle fallbacks for mapped Entity/Document namespaces.
func (idx *Index) ModelAliases() ([]ModelAlias, error) {
	if idx == nil {
		return nil, nil
	}
	models, err := idx.Models()
	if err != nil {
		return nil, err
	}
	idx.namespaceProviderMu.RLock()
	provider := idx.namespaceProvider
	idx.namespaceProviderMu.RUnlock()
	var configured map[string][]string
	var providerRevision uint64
	if provider != nil {
		configured, providerRevision =
			provider.GetDoctrineNamespaceAliasesState()
	}
	modelGeneration := idx.currentModelGeneration()

	idx.aliasCacheMu.Lock()
	defer idx.aliasCacheMu.Unlock()
	if idx.aliasCacheSet &&
		idx.aliasCacheGeneration == modelGeneration &&
		idx.aliasCacheProviderRevision == providerRevision {
		return append([]ModelAlias(nil), idx.cachedModelAliases...), nil
	}

	result := buildModelAliases(models, configured)
	targets := make(map[string][]string)
	for _, alias := range result {
		key := strings.ToLower(alias.Name)
		targets[key] = appendUniqueClass(targets[key], alias.Class)
	}
	idx.cachedModelAliases = append([]ModelAlias(nil), result...)
	idx.cachedAliasTargets = targets
	idx.aliasCacheGeneration = modelGeneration
	idx.aliasCacheProviderRevision = providerRevision
	idx.aliasCacheSet = true
	return append([]ModelAlias(nil), result...), nil
}

// ResolveModelName resolves either an FQCN or a Bundle:Model shortcut. A
// shortcut is accepted only when it identifies exactly one mapped model.
func (idx *Index) ResolveModelName(name string) (string, bool, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", false, nil
	}
	if !strings.Contains(name, ":") {
		normalized := normalizeClass(name)
		models, err := idx.Models()
		if err != nil {
			return "", false, err
		}
		for _, model := range models {
			if sameClass(model.Class, normalized) {
				return model.Class, true, nil
			}
		}
		return normalized, false, nil
	}
	if _, err := idx.ModelAliases(); err != nil {
		return "", false, err
	}
	idx.aliasCacheMu.Lock()
	targets := append(
		[]string(nil),
		idx.cachedAliasTargets[strings.ToLower(name)]...,
	)
	idx.aliasCacheMu.Unlock()
	if len(targets) != 1 {
		return name, false, nil
	}
	return targets[0], true, nil
}

func (idx *Index) canonicalModelName(name string) string {
	if resolved, found, err := idx.ResolveModelName(name); err == nil && found {
		return resolved
	}
	return normalizeClass(name)
}

// EntityNames includes both canonical class names and unambiguous shortcuts
// for typo suggestions and other string-oriented editor surfaces.
func (idx *Index) EntityNames() ([]string, error) {
	names, err := idx.ClassNames()
	if err != nil {
		return nil, err
	}
	aliases, err := idx.ModelAliases()
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{}, len(names)+len(aliases))
	result := make([]string, 0, len(names)+len(aliases))
	add := func(name string) {
		key := strings.ToLower(name)
		if name == "" {
			return
		}
		if _, exists := seen[key]; exists {
			return
		}
		seen[key] = struct{}{}
		result = append(result, name)
	}
	for _, name := range names {
		add(name)
	}
	for _, alias := range aliases {
		add(alias.Name)
	}
	sort.Slice(result, func(left, right int) bool {
		return strings.ToLower(result[left]) < strings.ToLower(result[right])
	})
	return result, nil
}

func buildModelAliases(
	models []Model,
	configured map[string][]string,
) []ModelAlias {
	var result []ModelAlias
	seen := make(map[string]struct{})
	add := func(alias ModelAlias) {
		alias.Name = strings.TrimSpace(alias.Name)
		alias.Class = normalizeClass(alias.Class)
		alias.Namespace = normalizeClass(alias.Namespace)
		if alias.Name == "" || alias.Class == "" {
			return
		}
		key := strings.ToLower(alias.Name) + "|" +
			strings.ToLower(alias.Class)
		if _, exists := seen[key]; exists {
			return
		}
		seen[key] = struct{}{}
		result = append(result, alias)
	}

	configuredAliases := make([]string, 0, len(configured))
	for alias := range configured {
		configuredAliases = append(configuredAliases, alias)
	}
	sort.Slice(configuredAliases, func(left, right int) bool {
		return strings.ToLower(configuredAliases[left]) <
			strings.ToLower(configuredAliases[right])
	})
	for _, alias := range configuredAliases {
		for _, namespace := range configured[alias] {
			namespace = normalizeClass(namespace)
			for _, model := range models {
				if !shortcutModel(model) {
					continue
				}
				relative, found := relativeDoctrineClass(
					model.Class,
					namespace,
				)
				if !found {
					continue
				}
				add(ModelAlias{
					Name:      alias + ":" + relative,
					Class:     model.Class,
					Namespace: namespace,
				})
			}
		}
	}

	for _, model := range models {
		if !shortcutModel(model) {
			continue
		}
		alias, namespace, relative, found := weakDoctrineModelAlias(
			model.Class,
		)
		if !found {
			continue
		}
		add(ModelAlias{
			Name:      alias + ":" + relative,
			Class:     model.Class,
			Namespace: namespace,
			Weak:      true,
		})
	}
	sort.Slice(result, func(left, right int) bool {
		if !strings.EqualFold(result[left].Name, result[right].Name) {
			return strings.ToLower(result[left].Name) <
				strings.ToLower(result[right].Name)
		}
		return strings.ToLower(result[left].Class) <
			strings.ToLower(result[right].Class)
	})
	return result
}

func shortcutModel(model Model) bool {
	return model.Kind != MappedSuperclassModel &&
		model.Kind != EmbeddableModel && model.Class != ""
}

func relativeDoctrineClass(className, namespace string) (string, bool) {
	className = normalizeClass(className)
	namespace = strings.TrimSuffix(normalizeClass(namespace), `\`)
	if len(className) <= len(namespace) ||
		!strings.EqualFold(className[:len(namespace)], namespace) ||
		className[len(namespace)] != '\\' {
		return "", false
	}
	return className[len(namespace)+1:], true
}

func weakDoctrineModelAlias(
	className string,
) (alias, namespace, relative string, found bool) {
	parts := strings.Split(normalizeClass(className), `\`)
	for namespaceEnd, part := range parts {
		switch strings.ToLower(part) {
		case "entity", "document", "couchdocument":
		default:
			continue
		}
		if namespaceEnd == 0 || namespaceEnd+1 >= len(parts) {
			return "", "", "", false
		}
		bundleEnd := -1
		for position := namespaceEnd - 1; position >= 0; position-- {
			if strings.HasSuffix(
				strings.ToLower(parts[position]),
				"bundle",
			) {
				bundleEnd = position
				break
			}
		}
		if bundleEnd < 0 {
			return "", "", "", false
		}
		return strings.Join(parts[:bundleEnd+1], ""),
			strings.Join(parts[:namespaceEnd+1], `\`),
			strings.Join(parts[namespaceEnd+1:], `\`),
			true
	}
	return "", "", "", false
}

func appendUniqueClass(values []string, candidate string) []string {
	for _, value := range values {
		if sameClass(value, candidate) {
			return values
		}
	}
	return append(values, candidate)
}
