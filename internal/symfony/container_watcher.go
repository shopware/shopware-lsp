package symfony

import (
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"

	xmlparser "github.com/shopware/shopware-lsp/internal/parser/xml"
	xmlsyntax "github.com/shopware/shopware-lsp/internal/parser/xml/syntax"
)

// ContainerWatcher watches the Symfony container XML file and keeps services in memory
type ContainerWatcher struct {
	projectRoot     string
	containerPath   string
	watcher         *fsnotify.Watcher
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

// NewContainerWatcher creates a new watcher for the Symfony container XML file
func NewContainerWatcher(projectRoot string) (*ContainerWatcher, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	cw := &ContainerWatcher{
		projectRoot:     projectRoot,
		watcher:         watcher,
		services:        make(map[string]Service),
		parameters:      make(map[string]Parameter),
		doctrineAliases: make(map[string][]string),
	}

	// Find and load the container file initially
	if err := cw.findAndLoadContainer(); err != nil {
		log.Printf("Initial container load failed: %v", err)
	}

	// Start watching for changes
	go cw.watchChanges()

	return cw, nil
}

// findAndLoadContainer locates and loads the Symfony container XML file
func (cw *ContainerWatcher) findAndLoadContainer() error {
	// Look for the container file in the var/cache directory
	containerPath, err := cw.findContainerFile()
	if err != nil {
		cw.mu.Lock()
		cw.containerExists = false
		cw.mu.Unlock()

		// Even if we can't find the container file, watch the var/cache directory
		// for when it might be created later
		cacheDir := filepath.Join(cw.projectRoot, "var", "cache")

		// Check if the cache directory exists
		if _, err := os.Stat(cacheDir); err == nil {
			// Watch the cache directory
			if err := cw.watcher.Add(cacheDir); err != nil {
				log.Printf("Failed to watch cache directory: %v", err)
			} else {
				log.Printf("Watching cache directory for container file creation")
			}

			// Also try to watch dev subdirectories if they exist
			entries, err := os.ReadDir(cacheDir)
			if err == nil {
				for _, entry := range entries {
					if entry.IsDir() && strings.HasPrefix(entry.Name(), "dev") {
						devDir := filepath.Join(cacheDir, entry.Name())
						if err := cw.watcher.Add(devDir); err != nil {
							log.Printf("Failed to watch dev directory %s: %v", devDir, err)
						} else {
							log.Printf("Watching dev directory %s for container file creation", devDir)
						}
					}
				}
			}
		}

		return err
	}

	cw.mu.Lock()
	cw.containerPath = containerPath
	cw.containerExists = true
	cw.mu.Unlock()

	// Add the directory to the watcher
	containerDir := filepath.Dir(containerPath)
	if err := cw.watcher.Add(containerDir); err != nil {
		return err
	}

	// Load the container file
	return cw.loadContainer()
}

// findContainerFile searches for the Symfony container XML file
func (cw *ContainerWatcher) findContainerFile() (string, error) {
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
func (cw *ContainerWatcher) loadContainer() error {
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

// watchChanges monitors the container file for changes
func (cw *ContainerWatcher) watchChanges() {
	for {
		select {
		case event, ok := <-cw.watcher.Events:
			if !ok {
				return
			}

			// Check if the event is for our container file
			containerPath, containerExists := cw.containerState()
			if containerExists && event.Name == containerPath && (event.Op&(fsnotify.Write|fsnotify.Create) != 0) {
				log.Printf("Container file changed, reloading")
				if err := cw.loadContainer(); err != nil {
					log.Printf("Failed to reload container: %v", err)
				}
			} else if !containerExists && strings.HasSuffix(event.Name, "Shopware_Core_KernelDevDebugContainer.xml") && (event.Op&fsnotify.Create != 0) {
				// Container file was created
				log.Printf("Container file created: %s", event.Name)
				cw.mu.Lock()
				cw.containerPath = event.Name
				cw.containerExists = true
				cw.mu.Unlock()
				if err := cw.loadContainer(); err != nil {
					log.Printf("Failed to load new container: %v", err)
				}
			}

		case err, ok := <-cw.watcher.Errors:
			if !ok {
				return
			}
			log.Printf("Watcher error: %v", err)
		}
	}
}

// GetServiceByID returns a service by ID from memory
func (cw *ContainerWatcher) GetServiceByID(id string) (Service, bool) {
	cw.mu.RLock()
	defer cw.mu.RUnlock()
	service, found := cw.services[id]
	return service, found
}

// GetParameterByName returns a parameter by name from memory
func (cw *ContainerWatcher) GetParameterByName(name string) (Parameter, bool) {
	cw.mu.RLock()
	defer cw.mu.RUnlock()
	param, found := cw.parameters[name]
	return param, found
}

// GetAllServices returns all services from memory
func (cw *ContainerWatcher) GetAllServices() []string {
	cw.mu.RLock()
	defer cw.mu.RUnlock()

	result := make([]string, 0, len(cw.services))
	for id := range cw.services {
		result = append(result, id)
	}

	return result
}

func (cw *ContainerWatcher) GetAllServiceDefinitions() []Service {
	cw.mu.RLock()
	defer cw.mu.RUnlock()

	result := make([]Service, 0, len(cw.services))
	for _, service := range cw.services {
		result = append(result, service)
	}
	return result
}

func (cw *ContainerWatcher) GetAllParameters() []Parameter {
	cw.mu.RLock()
	defer cw.mu.RUnlock()

	result := make([]Parameter, 0, len(cw.parameters))
	for _, parameter := range cw.parameters {
		result = append(result, parameter)
	}
	return result
}

func (cw *ContainerWatcher) GetTwigComponents() []ContainerTwigComponent {
	components, _ := cw.GetTwigComponentsState()
	return components
}

func (cw *ContainerWatcher) GetTwigComponentsState() (
	[]ContainerTwigComponent,
	uint64,
) {
	cw.mu.RLock()
	defer cw.mu.RUnlock()
	return append([]ContainerTwigComponent(nil), cw.twigComponents...),
		cw.revision
}

func (cw *ContainerWatcher) GetTwigGlobals() []ContainerTwigGlobal {
	cw.mu.RLock()
	defer cw.mu.RUnlock()
	return append([]ContainerTwigGlobal(nil), cw.twigGlobals...)
}

func (cw *ContainerWatcher) GetDoctrineNamespaceAliasesState() (
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
func (cw *ContainerWatcher) Close() error {
	return cw.watcher.Close()
}

// ContainerExists returns true if the container file exists
func (cw *ContainerWatcher) ContainerExists() bool {
	cw.mu.RLock()
	defer cw.mu.RUnlock()
	return cw.containerExists
}

// LastUpdated returns the time when the container was last updated
func (cw *ContainerWatcher) LastUpdated() time.Time {
	cw.mu.RLock()
	defer cw.mu.RUnlock()
	return cw.lastUpdated
}

func (cw *ContainerWatcher) containerState() (string, bool) {
	cw.mu.RLock()
	defer cw.mu.RUnlock()
	return cw.containerPath, cw.containerExists
}
