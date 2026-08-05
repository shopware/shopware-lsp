package twig

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/extension"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

type TwigCommandProvider struct {
	extensionIndex *extension.ExtensionIndexer
	twigIndexer    *TwigIndexer
	projectRoot    string
}

func NewTwigCommandProvider(projectRoot string, extensionIndex *extension.ExtensionIndexer, twigIndexer *TwigIndexer) *TwigCommandProvider {
	return &TwigCommandProvider{
		extensionIndex: extensionIndex,
		twigIndexer:    twigIndexer,
		projectRoot:    projectRoot,
	}
}

func (t *TwigCommandProvider) GetCommands(ctx context.Context) map[string]lsp.CommandFunc {
	return map[string]lsp.CommandFunc{
		"shopware/twig/extendBlock":  t.extendBlock,
		"shopware/twig/getBlockDiff": t.getBlockDiff,
	}
}

func (t *TwigCommandProvider) extendBlock(ctx context.Context, args *json.RawMessage) (interface{}, error) {
	var params struct {
		TextUri   string `json:"textUri"`
		BlockName string `json:"blockName"`
		Extension string `json:"extension"`
	}

	if err := json.Unmarshal(*args, &params); err != nil {
		return nil, err
	}

	extension, found, err := t.extensionIndex.FindByName(params.Extension)
	if err != nil {
		return nil, fmt.Errorf("find extension: %w", err)
	}
	if !found {
		return protocol.NewLspError("Extension not found", "extension.not_found"), nil
	}

	originalPath, err := uriutil.Path(params.TextUri)
	if err != nil {
		return nil, fmt.Errorf("resolve document URI: %w", err)
	}

	resourcesIndex := strings.Index(originalPath, "Resources/views/storefront")
	if resourcesIndex == -1 {
		return protocol.NewLspError("Not a storefront view file", "view.not_storefront"), nil
	}

	storefrontRelativePath := originalPath[resourcesIndex+16:]
	extensionViewPath := path.Join(extension.GetStorefrontViewsPath(), storefrontRelativePath)
	extensionViewPathDir := path.Dir(extensionViewPath)

	if _, err := os.Stat(extensionViewPathDir); os.IsNotExist(err) {
		if err := os.MkdirAll(extensionViewPathDir, 0755); err != nil {
			log.Printf("Failed to create directory: %s", extensionViewPathDir)
			return protocol.NewLspError("Failed to create directory", "directory.create_failed"), nil
		}
	}

	_, err = os.Stat(extensionViewPath)

	if os.IsNotExist(err) {
		if err := os.WriteFile(extensionViewPath, []byte("{% sw_extends \"@Storefront/"+storefrontRelativePath+"\" %}\n"), 0644); err != nil {
			log.Printf("Failed to create file: %s", extensionViewPath)
			return protocol.NewLspError("Failed to create file", "file.create_failed"), nil
		}
	}

	currentContent, err := os.ReadFile(extensionViewPath)

	if err != nil {
		return protocol.NewLspError("Failed to read file", "file.read_failed"), nil
	}

	twigFile, err := ParseTwig(extensionViewPath, currentContent)
	if err != nil {
		return protocol.NewLspError("Failed to parse twig file", "parse.failed"), nil
	}
	if _, exists := twigFile.Blocks[params.BlockName]; exists {
		return protocol.NewLspError("Block already exists", "block.already_exists"), nil
	}

	versionComment := ""
	if t.twigIndexer != nil {
		if allBlockHashes, err := t.twigIndexer.GetTwigBlockHashes(params.BlockName); err == nil {
			if originalHash := FindOriginalStorefrontHash(allBlockHashes); originalHash != nil {
				versionComment = FormatVersionComment(originalHash.Hash, DetectShopwareVersion(t.projectRoot))
			}
		}
	}

	currentContent = append(currentContent, []byte("\n\n"+versionComment+"{% block "+params.BlockName+" %}\n\n{% endblock %}\n")...)
	if err := os.WriteFile(extensionViewPath, currentContent, 0644); err != nil {
		log.Printf("Failed to write file: %s", extensionViewPath)
		return protocol.NewLspError("Failed to write file", "file.write_failed"), nil
	}

	twigFile, err = ParseTwig(extensionViewPath, currentContent)
	if err != nil {
		return protocol.NewLspError("Failed to parse twig file", "parse.failed"), nil
	}
	block, exists := twigFile.Blocks[params.BlockName]
	if !exists {
		return protocol.NewLspError("Block not found after creation", "block.not_found"), nil
	}

	return map[string]any{
		"uri":  uriutil.FileURI(extensionViewPath),
		"line": block.Line,
	}, nil
}

func (t *TwigCommandProvider) getBlockDiff(ctx context.Context, args *json.RawMessage) (interface{}, error) {
	var params struct {
		TextUri   string `json:"textUri"`
		BlockName string `json:"blockName"`
	}

	if err := json.Unmarshal(*args, &params); err != nil {
		return nil, err
	}

	if t.twigIndexer == nil {
		return protocol.NewLspError("Twig indexer not available", "indexer.not_available"), nil
	}

	allBlockHashes, err := t.twigIndexer.GetTwigBlockHashes(params.BlockName)
	if err != nil {
		return protocol.NewLspError("Failed to get block hashes", "block.hash_failed"), nil
	}

	currentBlock := FindOriginalStorefrontHash(allBlockHashes)
	if currentBlock == nil {
		return protocol.NewLspError("Original block not found", "block.not_found"), nil
	}

	filePath, err := uriutil.Path(params.TextUri)
	if err != nil {
		return nil, fmt.Errorf("resolve document URI: %w", err)
	}
	overrideContent, err := os.ReadFile(filePath)
	if err != nil {
		return protocol.NewLspError("Failed to read override file", "file.read_failed"), nil
	}

	twigFile, err := ParseTwig(filePath, overrideContent)
	if err != nil {
		return protocol.NewLspError("Failed to parse twig file", "parse.failed"), nil
	}

	block, exists := twigFile.Blocks[params.BlockName]
	if !exists {
		return protocol.NewLspError("Block not found in override file", "block.not_found"), nil
	}

	if block.VersionComment == nil {
		return protocol.NewLspError("No version comment found for block", "version.not_found"), nil
	}

	versionComment := block.VersionComment

	originalContent, err := t.getBlockContentAtVersion(currentBlock.AbsolutePath, params.BlockName, versionComment.Version)
	if err != nil {
		return protocol.NewLspError(fmt.Sprintf("Failed to get block at version %s: %v", versionComment.Version, err), "git.failed"), nil
	}

	return map[string]any{
		"blockName":       params.BlockName,
		"originalContent": originalContent,
		"originalVersion": versionComment.Version,
		"currentContent":  currentBlock.Text,
		"currentVersion":  DetectShopwareVersion(t.projectRoot),
	}, nil
}

func (t *TwigCommandProvider) getBlockContentAtVersion(absolutePath, blockName, version string) (string, error) {
	storefrontPath := t.findStorefrontPackagePath()
	if storefrontPath == "" {
		return "", fmt.Errorf("storefront package not found")
	}

	relativePath := strings.TrimPrefix(absolutePath, storefrontPath)
	relativePath = strings.TrimPrefix(relativePath, "/")

	gitRef := version
	if !strings.HasPrefix(gitRef, "v") {
		gitRef = "v" + gitRef
	}

	fileContent, err := t.getFileContentFromGit(storefrontPath, relativePath, gitRef)
	if err != nil {
		fileContent, err = t.getFileContentFromGitHub(relativePath, gitRef)
		if err != nil {
			return "", err
		}
	}

	twigFile, err := ParseTwig(relativePath, []byte(fileContent))
	if err != nil {
		return "", fmt.Errorf("failed to parse block %s at version %s: %w", blockName, version, err)
	}
	block, exists := twigFile.Blocks[blockName]
	if !exists {
		return "", fmt.Errorf("block %s not found at version %s", blockName, version)
	}

	return block.Text, nil
}

func (t *TwigCommandProvider) findStorefrontPackagePath() string {
	possiblePaths := []string{
		filepath.Join(t.projectRoot, "vendor", "shopware", "storefront"),
		filepath.Join(t.projectRoot, "vendor", "shopware", "platform", "src", "Storefront"),
		filepath.Join(t.projectRoot, "src", "Storefront"),
	}

	for _, p := range possiblePaths {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}

	return ""
}

func (t *TwigCommandProvider) getFileContentFromGit(repoPath, filePath, ref string) (string, error) {
	cmd := exec.Command("git", "show", fmt.Sprintf("%s:%s", ref, filePath))
	cmd.Dir = repoPath

	output, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return "", fmt.Errorf("git show failed: %s", string(exitErr.Stderr))
		}
		return "", err
	}

	return string(output), nil
}

func (t *TwigCommandProvider) getFileContentFromGitHub(relativePath, version string) (string, error) {
	githubPath := "src/Storefront/" + relativePath

	url := fmt.Sprintf("https://raw.githubusercontent.com/shopware/shopware/%s/%s", version, githubPath)

	resp, err := http.Get(url)
	if err != nil {
		return "", fmt.Errorf("failed to fetch from GitHub: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitHub returned status %d for version %s", resp.StatusCode, version)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read GitHub response: %v", err)
	}

	return string(body), nil
}
