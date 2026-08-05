package shopware

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/shopware/shopware-lsp/internal/php/project"
)

type VersionSource string

const (
	VersionSourceExplicit            VersionSource = "explicit"
	VersionSourceComposerLock        VersionSource = "composer-lock"
	VersionSourcePlatformComposer    VersionSource = "platform-composer"
	VersionSourcePlatformKernel      VersionSource = "platform-kernel"
	VersionSourcePlatformBranchAlias VersionSource = "platform-branch-alias"
)

// ResolvedVersion identifies both a Shopware version and the evidence used to
// select it. Known is false when the workspace does not provide enough stable
// evidence; callers should disable version-gated behavior in that case.
type ResolvedVersion struct {
	Version project.Version
	Source  VersionSource
	Known   bool
}

func (v ResolvedVersion) AtLeast(major, minor, patch int) bool {
	return v.Known && v.Version.AtLeastPatch(major, minor, patch)
}

type VersionResolver struct {
	root           string
	project        *project.Model
	explicitTarget string
}

func NewVersionResolver(
	root string,
	model *project.Model,
	explicitTarget string,
) *VersionResolver {
	return &VersionResolver{
		root:           root,
		project:        model,
		explicitTarget: strings.TrimSpace(explicitTarget),
	}
}

func (r *VersionResolver) Resolve() (ResolvedVersion, error) {
	if r == nil {
		return ResolvedVersion{}, errors.New("resolve Shopware version: resolver is nil")
	}
	if r.explicitTarget != "" {
		version, ok := parseExplicitVersion(r.explicitTarget)
		if !ok {
			return ResolvedVersion{}, fmt.Errorf(
				"resolve Shopware version: invalid explicit target %q; expected major.minor[.patch[.build]]",
				r.explicitTarget,
			)
		}
		return knownVersion(version, VersionSourceExplicit), nil
	}
	if r.project != nil {
		if version, found := r.project.DependencyVersion(
			"shopware/core",
			"shopware/platform",
		); found {
			return knownVersion(version, VersionSourceComposerLock), nil
		}
	}
	return r.resolvePlatformCheckout()
}

type platformComposer struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Extra   struct {
		BranchAlias map[string]string `json:"branch-alias"`
	} `json:"extra"`
}

func (r *VersionResolver) resolvePlatformCheckout() (ResolvedVersion, error) {
	if strings.TrimSpace(r.root) == "" {
		return ResolvedVersion{}, nil
	}
	content, err := os.ReadFile(filepath.Join(r.root, "composer.json"))
	if errors.Is(err, os.ErrNotExist) {
		return ResolvedVersion{}, nil
	}
	if err != nil {
		return ResolvedVersion{}, fmt.Errorf("resolve Shopware version: read root composer.json: %w", err)
	}
	var composer platformComposer
	if err := json.Unmarshal(content, &composer); err != nil {
		return ResolvedVersion{}, fmt.Errorf("resolve Shopware version: parse root composer.json: %w", err)
	}
	if !strings.EqualFold(strings.TrimSpace(composer.Name), "shopware/platform") {
		return ResolvedVersion{}, nil
	}
	if version, ok := project.ParseVersionConstraint(composer.Version); ok {
		return knownVersion(version, VersionSourcePlatformComposer), nil
	}
	if version, found, err := platformKernelVersion(r.root); err != nil {
		return ResolvedVersion{}, err
	} else if found {
		return knownVersion(version, VersionSourcePlatformKernel), nil
	}
	if version, found := consistentBranchAliasVersion(composer.Extra.BranchAlias); found {
		return knownVersion(version, VersionSourcePlatformBranchAlias), nil
	}
	return ResolvedVersion{}, nil
}

func knownVersion(version project.Version, source VersionSource) ResolvedVersion {
	return ResolvedVersion{Version: version, Source: source, Known: true}
}

var (
	explicitVersionPattern = regexp.MustCompile(
		`(?i)^v?(\d+)\.(\d+)(?:\.(\d+))?(?:\.\d+)?(?:[-+][0-9a-z.-]+)?$`,
	)
	kernelFallbackVersionPattern = regexp.MustCompile(
		`(?m)^[\t ]*(?:(?:final|public|protected|private)[\t ]+)*const[\t ]+SHOPWARE_FALLBACK_VERSION[\t ]*=[\t ]*['"]([^'"]+)['"]`,
	)
)

func parseExplicitVersion(source string) (project.Version, bool) {
	match := explicitVersionPattern.FindStringSubmatch(strings.TrimSpace(source))
	if len(match) == 0 {
		return project.Version{}, false
	}
	major, majorErr := strconv.Atoi(match[1])
	minor, minorErr := strconv.Atoi(match[2])
	patch := 0
	var patchErr error
	if match[3] != "" {
		patch, patchErr = strconv.Atoi(match[3])
	}
	if majorErr != nil || minorErr != nil || patchErr != nil {
		return project.Version{}, false
	}
	return project.Version{Major: major, Minor: minor, Patch: patch}, true
}

func platformKernelVersion(root string) (project.Version, bool, error) {
	content, err := os.ReadFile(filepath.Join(root, "src", "Core", "Kernel.php"))
	if errors.Is(err, os.ErrNotExist) {
		return project.Version{}, false, nil
	}
	if err != nil {
		return project.Version{}, false, fmt.Errorf(
			"resolve Shopware version: read platform Kernel.php: %w",
			err,
		)
	}
	match := kernelFallbackVersionPattern.FindSubmatch(content)
	if len(match) < 2 {
		return project.Version{}, false, nil
	}
	version, ok := project.ParseVersionConstraint(string(match[1]))
	return version, ok, nil
}

func consistentBranchAliasVersion(aliases map[string]string) (project.Version, bool) {
	var selected project.Version
	found := false
	for _, alias := range aliases {
		version, ok := project.ParseVersionConstraint(alias)
		if !ok {
			continue
		}
		if !found {
			selected = version
			found = true
			continue
		}
		if selected.Compare(version) != 0 {
			return project.Version{}, false
		}
	}
	return selected, found
}
