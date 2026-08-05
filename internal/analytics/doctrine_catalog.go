package analytics

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/doctrine"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

const (
	ListDoctrineEntitiesCommand = "shopware/symfony/analytics/doctrine/entities"
	ListDoctrineFieldsCommand   = "shopware/symfony/analytics/doctrine/entityFields"
)

type DoctrineCatalogProvider struct {
	root  string
	index *doctrine.Index
}

func NewDoctrineCatalogProvider(
	root string,
	index *doctrine.Index,
) *DoctrineCatalogProvider {
	return &DoctrineCatalogProvider{
		root:  filepath.Clean(root),
		index: index,
	}
}

type DoctrineEntityCatalogRequest struct {
	Query    string `json:"query,omitempty"`
	Kind     string `json:"kind,omitempty"`
	FileGlob string `json:"fileGlob,omitempty"`
}

type DoctrineEntityCatalogEntry struct {
	Class                 string `json:"class"`
	Parent                string `json:"parent,omitempty"`
	Repository            string `json:"repository,omitempty"`
	Table                 string `json:"table,omitempty"`
	Kind                  string `json:"kind"`
	Source                string `json:"source"`
	FileURI               string `json:"fileUri,omitempty"`
	SourceLine            int    `json:"sourceLine,omitempty"`
	FieldCount            int    `json:"fieldCount"`
	IndexCount            int    `json:"indexCount"`
	UniqueConstraintCount int    `json:"uniqueConstraintCount"`
}

type DoctrineFieldCatalogRequest struct {
	ClassName string `json:"className"`
}

type DoctrineFieldCatalogEntry struct {
	Name           string   `json:"name"`
	Column         string   `json:"column,omitempty"`
	Type           string   `json:"type,omitempty"`
	Relation       string   `json:"relation,omitempty"`
	RelationType   string   `json:"relationType,omitempty"`
	EnumType       string   `json:"enumType,omitempty"`
	PHPType        string   `json:"phpType,omitempty"`
	PropertyTypes  []string `json:"propertyTypes,omitempty"`
	EmbeddedClass  string   `json:"embeddedClass,omitempty"`
	ColumnPrefix   string   `json:"columnPrefix,omitempty"`
	DeclaringClass string   `json:"declaringClass,omitempty"`
	FileURI        string   `json:"fileUri,omitempty"`
	SourceLine     int      `json:"sourceLine,omitempty"`
}

func (p *DoctrineCatalogProvider) GetCommands(
	_ context.Context,
) map[string]lsp.CommandFunc {
	return map[string]lsp.CommandFunc{
		ListDoctrineEntitiesCommand: p.listEntities,
		ListDoctrineFieldsCommand:   p.listFields,
	}
}

func (p *DoctrineCatalogProvider) listEntities(
	ctx context.Context,
	raw *json.RawMessage,
) (interface{}, error) {
	var request DoctrineEntityCatalogRequest
	if raw != nil && len(*raw) != 0 && string(*raw) != "null" {
		if err := json.Unmarshal(*raw, &request); err != nil {
			return nil, fmt.Errorf(
				"invalid Doctrine entity catalog request: %w",
				err,
			)
		}
	}
	return p.Entities(ctx, request)
}

func (p *DoctrineCatalogProvider) listFields(
	ctx context.Context,
	raw *json.RawMessage,
) (interface{}, error) {
	var request DoctrineFieldCatalogRequest
	if raw == nil || len(*raw) == 0 || string(*raw) == "null" {
		return nil, fmt.Errorf("doctrine entity className is required")
	}
	if err := json.Unmarshal(*raw, &request); err != nil {
		return nil, fmt.Errorf(
			"invalid Doctrine field catalog request: %w",
			err,
		)
	}
	return p.Fields(ctx, request)
}

func (p *DoctrineCatalogProvider) Entities(
	ctx context.Context,
	request DoctrineEntityCatalogRequest,
) ([]DoctrineEntityCatalogEntry, error) {
	if p == nil || p.index == nil {
		return nil, fmt.Errorf("doctrine entity catalog is unavailable")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	models, err := p.index.Models()
	if err != nil {
		return nil, err
	}
	query := strings.ToLower(strings.TrimSpace(request.Query))
	kind := normalizeDoctrineKind(request.Kind)
	fileGlob := filepath.ToSlash(strings.TrimSpace(request.FileGlob))
	lines := newSourceLineResolver()
	result := make([]DoctrineEntityCatalogEntry, 0, len(models))
	for _, model := range models {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		entry, entryErr := p.entityEntry(model, lines)
		if entryErr != nil {
			return nil, entryErr
		}
		if kind != "" && entry.Kind != kind {
			continue
		}
		if query != "" && !doctrineEntityMatches(entry, query) {
			continue
		}
		if fileGlob != "" {
			relative := p.relativePath(model.File)
			if relative == "" || !antPathMatch(fileGlob, relative) {
				continue
			}
		}
		result = append(result, entry)
	}
	sort.Slice(result, func(left, right int) bool {
		return strings.ToLower(result[left].Class) <
			strings.ToLower(result[right].Class)
	})
	return result, nil
}

func (p *DoctrineCatalogProvider) Fields(
	ctx context.Context,
	request DoctrineFieldCatalogRequest,
) ([]DoctrineFieldCatalogEntry, error) {
	if p == nil || p.index == nil {
		return nil, fmt.Errorf("doctrine entity catalog is unavailable")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	className := strings.TrimPrefix(
		strings.TrimSpace(request.ClassName),
		`\`,
	)
	if className == "" {
		return nil, fmt.Errorf("doctrine entity className is required")
	}
	model, found, err := p.index.Model(className)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("doctrine entity %q was not found", className)
	}
	fields, err := p.index.Fields(model.Class)
	if err != nil {
		return nil, err
	}
	if len(fields) == 0 {
		return nil, fmt.Errorf(
			"doctrine entity %q has no mapped fields",
			model.Class,
		)
	}
	lines := newSourceLineResolver()
	result := make([]DoctrineFieldCatalogEntry, 0, len(fields))
	for _, field := range fields {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		entry := DoctrineFieldCatalogEntry{
			Name:           field.Name,
			Column:         field.Column,
			Type:           field.Type,
			Relation:       field.Relation,
			RelationType:   field.RelationType,
			EnumType:       field.EnumType,
			PHPType:        field.PHPType,
			PropertyTypes:  doctrinePropertyTypes(field.PHPType),
			EmbeddedClass:  field.EmbeddedClass,
			ColumnPrefix:   field.ColumnPrefix,
			DeclaringClass: field.Class,
		}
		if field.File != "" {
			entry.FileURI = uriutil.FileURI(field.File)
			entry.SourceLine = lines.line(field.File, field.Range.Start)
		}
		result = append(result, entry)
	}
	return result, nil
}

func (p *DoctrineCatalogProvider) entityEntry(
	model doctrine.Model,
	lines *sourceLineResolver,
) (DoctrineEntityCatalogEntry, error) {
	fields, err := p.index.Fields(model.Class)
	if err != nil {
		return DoctrineEntityCatalogEntry{}, err
	}
	entry := DoctrineEntityCatalogEntry{
		Class:      model.Class,
		Parent:     model.Parent,
		Repository: model.Repository,
		Table:      model.Table,
		Kind:       doctrineModelKind(model.Kind),
		Source:     doctrineSourceKind(model.Source),
		FieldCount: len(fields),
	}
	for _, constraint := range model.TableConstraints {
		if constraint.Kind == doctrine.UniqueConstraint {
			entry.UniqueConstraintCount++
		} else {
			entry.IndexCount++
		}
	}
	if model.File != "" {
		entry.FileURI = uriutil.FileURI(model.File)
		offset := model.NameRange.Start
		if model.NameRange.Len() == 0 {
			offset = model.Range.Start
		}
		entry.SourceLine = lines.line(model.File, offset)
	}
	return entry, nil
}

func doctrineEntityMatches(
	entry DoctrineEntityCatalogEntry,
	query string,
) bool {
	haystack := strings.ToLower(strings.Join([]string{
		entry.Class,
		entry.Parent,
		entry.Repository,
		entry.Table,
		entry.Kind,
		entry.Source,
	}, "\n"))
	return strings.Contains(haystack, query)
}

func normalizeDoctrineKind(kind string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "entity":
		return "entity"
	case "mapped superclass", "mappedsuperclass", "mapped-superclass":
		return "mappedSuperclass"
	case "embeddable":
		return "embeddable"
	case "document":
		return "document"
	default:
		return strings.TrimSpace(kind)
	}
}

func doctrineModelKind(kind doctrine.ModelKind) string {
	switch kind {
	case doctrine.MappedSuperclassModel:
		return "mappedSuperclass"
	case doctrine.EmbeddableModel:
		return "embeddable"
	case doctrine.DocumentModel:
		return "document"
	default:
		return "entity"
	}
}

func doctrineSourceKind(source doctrine.SourceKind) string {
	switch source {
	case doctrine.PHPAnnotationSource:
		return "phpAnnotation"
	case doctrine.XMLSource:
		return "xml"
	case doctrine.YAMLSource:
		return "yaml"
	default:
		return "phpAttribute"
	}
}

func doctrinePropertyTypes(phpType string) []string {
	if phpType == "" {
		return nil
	}
	var result []string
	seen := make(map[string]struct{})
	for _, value := range strings.Split(phpType, "|") {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, value)
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func (p *DoctrineCatalogProvider) relativePath(path string) string {
	if path == "" {
		return ""
	}
	relative, err := filepath.Rel(p.root, path)
	if err != nil || relative == ".." ||
		strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return ""
	}
	return filepath.ToSlash(relative)
}

var _ lsp.CommandProvider = (*DoctrineCatalogProvider)(nil)
