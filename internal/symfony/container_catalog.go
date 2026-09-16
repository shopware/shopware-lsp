package symfony

import (
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	xmlparser "github.com/shopware/shopware-lsp/internal/parser/xml"
	xmlsyntax "github.com/shopware/shopware-lsp/internal/parser/xml/syntax"
)

// ContainerCatalog holds the selected compiled container snapshot. FileScanner owns updates.
type ContainerCatalog struct {
	projectRoot     string
	containerPath   string
	services        map[string]Service
	parameters      map[string]Parameter
	twigGlobals     []ContainerTwigGlobal
	twigComponents  []ContainerTwigComponent
	doctrineAliases map[string][]string
	revision        uint64
	mu              sync.RWMutex
	lastUpdated     time.Time
	containerExists bool
}

// NewContainerCatalog creates an empty compiled container catalog.
func NewContainerCatalog(projectRoot string) (*ContainerCatalog, error) {
	cw := &ContainerCatalog{
		projectRoot:     projectRoot,
		services:        make(map[string]Service),
		parameters:      make(map[string]Parameter),
		doctrineAliases: make(map[string][]string),
	}

	return cw, nil
}

// findAndLoadContainer locates and loads the Symfony container XML file
func (cw *ContainerCatalog) findAndLoadContainer() error {
	// Look for the container file in the var/cache directory
	containerPath, err := cw.findContainerFile()
	if err != nil {
		cw.mu.Lock()
		cw.containerExists = false
		cw.services = make(map[string]Service)
		cw.parameters = make(map[string]Parameter)
		cw.twigGlobals = nil
		cw.twigComponents = nil
		cw.doctrineAliases = nil
		cw.revision++
		cw.mu.Unlock()

		return err
	}

	cw.mu.Lock()
	cw.containerPath = containerPath
	cw.containerExists = true
	cw.mu.Unlock()

	// Load the container file
	return cw.loadContainer()
}

// findContainerFile searches for the Symfony container XML file
func (cw *ContainerCatalog) findContainerFile() (string, error) {
	cacheDir := filepath.Join(cw.projectRoot, "var", "cache")

	// Check if the cache directory exists
	if _, err := os.Stat(cacheDir); os.IsNotExist(err) {
		return "", err
	}

	// Pattern to match Shopware_Core_KernelDevDebugContainer.xml
	pattern := filepath.Join(cacheDir, "dev*", "Shopware_Core_KernelDevDebugContainer.xml")

	// Find matching files
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return "", err
	}

	// Use the first match if any
	if len(matches) > 0 {
		return matches[0], nil
	}

	return "", os.ErrNotExist
}

// loadContainer loads the container XML file into memory
func (cw *ContainerCatalog) loadContainer() error {
	// Read the file
	content, err := os.ReadFile(cw.containerPath)
	if err != nil {
		return err
	}

	tree := xmlparser.Parse(string(content)).Tree
	services, params, err := ParseXMLServicesTree(
		cw.containerPath,
		tree,
		xmlsyntax.NewLineIndex(tree.Source),
	)
	if err != nil {
		return err
	}
	twigGlobals := ParseXMLTwigGlobalsTree(cw.containerPath, tree)
	twigComponents := ParseXMLTwigComponentsTree(cw.containerPath, tree)
	doctrineAliases := ParseXMLDoctrineNamespaceAliasesTree(tree.Root)

	// Update the in-memory cache
	cw.mu.Lock()
	defer cw.mu.Unlock()

	// Clear existing data
	cw.services = make(map[string]Service, len(services))
	cw.parameters = make(map[string]Parameter, len(params))
	cw.twigGlobals = append([]ContainerTwigGlobal(nil), twigGlobals...)
	cw.twigComponents = append(
		[]ContainerTwigComponent(nil),
		twigComponents...,
	)
	cw.doctrineAliases = cloneDoctrineNamespaceAliases(doctrineAliases)

	// Store the new data
	for _, service := range services {
		cw.services[service.ID] = service
	}

	for _, param := range params {
		cw.parameters[param.Name] = param
	}

	cw.revision++
	cw.lastUpdated = time.Now()
	log.Printf("Loaded %d services and %d parameters from container XML",
		len(services), len(params))

	return nil
}

// GetServiceByID returns a service by ID from memory
func (cw *ContainerCatalog) GetServiceByID(id string) (Service, bool) {
	cw.mu.RLock()
	defer cw.mu.RUnlock()
	service, found := cw.services[id]
	return service, found
}

// GetParameterByName returns a parameter by name from memory
func (cw *ContainerCatalog) GetParameterByName(name string) (Parameter, bool) {
	cw.mu.RLock()
	defer cw.mu.RUnlock()
	param, found := cw.parameters[name]
	return param, found
}

// GetAllServices returns all services from memory
func (cw *ContainerCatalog) GetAllServices() []string {
	cw.mu.RLock()
	defer cw.mu.RUnlock()

	result := make([]string, 0, len(cw.services))
	for id := range cw.services {
		result = append(result, id)
	}

	return result
}

func (cw *ContainerCatalog) GetAllServiceDefinitions() []Service {
	cw.mu.RLock()
	defer cw.mu.RUnlock()

	result := make([]Service, 0, len(cw.services))
	for _, service := range cw.services {
		result = append(result, service)
	}
	return result
}

// Revision returns the catalog's mutation counter for cache invalidation.
func (cw *ContainerCatalog) Revision() uint64 {
	if cw == nil {
		return 0
	}
	cw.mu.RLock()
	defer cw.mu.RUnlock()
	return cw.revision
}

func (cw *ContainerCatalog) GetAllParameters() []Parameter {
	cw.mu.RLock()
	defer cw.mu.RUnlock()

	result := make([]Parameter, 0, len(cw.parameters))
	for _, parameter := range cw.parameters {
		result = append(result, parameter)
	}
	return result
}

func (cw *ContainerCatalog) GetTwigComponents() []ContainerTwigComponent {
	components, _ := cw.GetTwigComponentsState()
	return components
}

func (cw *ContainerCatalog) GetTwigComponentsState() (
	[]ContainerTwigComponent,
	uint64,
) {
	cw.mu.RLock()
	defer cw.mu.RUnlock()
	return append([]ContainerTwigComponent(nil), cw.twigComponents...),
		cw.revision
}

func (cw *ContainerCatalog) GetTwigGlobals() []ContainerTwigGlobal {
	cw.mu.RLock()
	defer cw.mu.RUnlock()
	return append([]ContainerTwigGlobal(nil), cw.twigGlobals...)
}

func (cw *ContainerCatalog) GetDoctrineNamespaceAliasesState() (
	map[string][]string,
	uint64,
) {
	cw.mu.RLock()
	defer cw.mu.RUnlock()
	return cloneDoctrineNamespaceAliases(cw.doctrineAliases), cw.revision
}

func cloneDoctrineNamespaceAliases(
	source map[string][]string,
) map[string][]string {
	result := make(map[string][]string, len(source))
	for alias, namespaces := range source {
		result[alias] = append([]string(nil), namespaces...)
	}
	return result
}

// Close stops the watcher and cleans up resources
func (cw *ContainerCatalog) Close() error {
	return nil
}

// ContainerExists returns true if the container file exists
func (cw *ContainerCatalog) ContainerExists() bool {
	cw.mu.RLock()
	defer cw.mu.RUnlock()
	return cw.containerExists
}

// LastUpdated returns the time when the container was last updated
func (cw *ContainerCatalog) LastUpdated() time.Time {
	cw.mu.RLock()
	defer cw.mu.RUnlock()
	return cw.lastUpdated
}
