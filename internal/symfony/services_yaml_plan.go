package symfony

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// ServicesXMLConversion describes one lossless XML-to-YAML file replacement.
// Planning is side-effect free so callers can validate and apply every file as
// one workspace edit.
type ServicesXMLConversion struct {
	SourcePath string
	TargetPath string
	Content    []byte
}

// PlanServicesXMLConversion converts a services.xml file and its local XML
// imports without changing the filesystem. read supplies the current document
// snapshots, including unsaved editor buffers. targetExists protects existing
// YAML configuration from being overwritten.
func PlanServicesXMLConversion(
	ctx context.Context,
	path string,
	read func(context.Context, string) ([]byte, error),
	targetExists func(string) (bool, error),
) ([]ServicesXMLConversion, error) {
	if read == nil {
		return nil, errors.New("services XML conversion has no document reader")
	}
	if targetExists == nil {
		return nil, errors.New("services XML conversion has no target checker")
	}

	planner := servicesXMLPlanner{
		ctx:          ctx,
		read:         read,
		targetExists: targetExists,
		planned:      make(map[string]string),
		contents:     make(map[string][]byte),
	}
	if err := planner.plan(filepath.Clean(path), nil); err != nil {
		return nil, err
	}

	paths := make([]string, 0, len(planner.planned))
	for source := range planner.planned {
		paths = append(paths, source)
	}
	sort.Strings(paths)

	result := make([]ServicesXMLConversion, 0, len(paths))
	for _, source := range paths {
		result = append(result, ServicesXMLConversion{
			SourcePath: source,
			TargetPath: planner.planned[source],
			Content:    append([]byte(nil), planner.contents[source]...),
		})
	}
	return result, nil
}

type servicesXMLPlanner struct {
	ctx          context.Context
	read         func(context.Context, string) ([]byte, error)
	targetExists func(string) (bool, error)
	planned      map[string]string
	contents     map[string][]byte
}

type servicesXMLImportReference struct {
	resource   *string
	loaderType *string
}

func (c *Container) importReferences() []servicesXMLImportReference {
	references := []servicesXMLImportReference{}
	importLists := []*xmlImports{c.Imports}
	for index := range c.When {
		importLists = append(importLists, c.When[index].Imports)
	}
	for _, imports := range importLists {
		if imports == nil {
			continue
		}
		for index := range imports.Imports {
			references = append(references, servicesXMLImportReference{
				resource:   &imports.Imports[index].Resource,
				loaderType: &imports.Imports[index].Type,
			})
		}
	}
	return references
}

func (p *servicesXMLPlanner) plan(path string, prefetched []byte) error {
	if err := p.ctx.Err(); err != nil {
		return err
	}
	if _, found := p.planned[path]; found {
		return nil
	}

	content := prefetched
	if content == nil {
		var err error
		content, err = p.read(p.ctx, path)
		if err != nil {
			return err
		}
	}
	container, err := ParseServicesXML(content)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}

	withoutExtension := strings.TrimSuffix(path, filepath.Ext(path))
	target := withoutExtension + ".yaml"
	for _, candidate := range []string{target, withoutExtension + ".yml"} {
		exists, existsErr := p.targetExists(candidate)
		if existsErr != nil {
			return fmt.Errorf("check conversion target %s: %w", candidate, existsErr)
		}
		if exists {
			return fmt.Errorf("cannot convert %s: %s exists already", path, candidate)
		}
	}

	// Mark the file before descending so circular local imports terminate.
	p.planned[path] = target
	for _, reference := range container.importReferences() {
		if err := p.ctx.Err(); err != nil {
			return err
		}
		resource := *reference.resource
		if !strings.HasSuffix(strings.ToLower(resource), ".xml") ||
			strings.ContainsAny(resource, "*?[{") || filepath.IsAbs(resource) {
			continue
		}

		importPath := filepath.Clean(filepath.Join(
			filepath.Dir(path), filepath.FromSlash(resource),
		))
		importContent, readErr := p.read(p.ctx, importPath)
		if readErr != nil {
			// Match Symfony's loader behavior: unresolved optional/local imports
			// remain XML references and do not make the root conversion lossy.
			continue
		}
		if err := p.plan(importPath, importContent); err != nil {
			return err
		}

		*reference.resource = resource[:len(resource)-len(".xml")] + ".yaml"
		if *reference.loaderType == "xml" {
			*reference.loaderType = "yaml"
		}
	}

	converted, err := ConvertContainerToYAML(container)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	p.contents[path] = converted
	return nil
}
