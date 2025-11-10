package dalec

import (
	"io/fs"
	"maps"
	"path/filepath"
)

// Artifacts describes all the artifacts to include in the package.
// Artifacts are broken down into types, e.g. binaries, manpages, etc.
// This differentiation is used to determine where to place the artifact on install.
type Artifacts struct {
	// Binaries is the list of binaries to include in the package.
	Binaries map[string]ArtifactConfig `yaml:"binaries,omitempty" json:"binaries,omitempty"`
	// Libexec is the list of additional binaries that may be invoked by the main package binary.
	Libexec map[string]ArtifactConfig `yaml:"libexec,omitempty" json:"libexec,omitempty"`
	// Manpages is the list of manpages to include in the package.
	Manpages map[string]ArtifactConfig `yaml:"manpages,omitempty" json:"manpages,omitempty"`
	// DataDirs is a list of read-only architecture-independent data files, to be placed in /usr/share/
	DataDirs map[string]ArtifactConfig `yaml:"data_dirs,omitempty" json:"data_dirs,omitempty"`
	// Directories is a list of various directories that should be created by the package.
	Directories *CreateArtifactDirectories `yaml:"createDirectories,omitempty" json:"createDirectories,omitempty"`
	// ConfigFiles is a list of files that should be marked as config files in the package.
	ConfigFiles map[string]ArtifactConfig `yaml:"configFiles,omitempty" json:"configFiles,omitempty"`
	// Docs is a list of doc files included in the package
	Docs map[string]ArtifactConfig `yaml:"docs,omitempty" json:"docs,omitempty"`
	// Licenses is a list of doc files included in the package
	Licenses map[string]ArtifactConfig `yaml:"licenses,omitempty" json:"licenses,omitempty"`
	// Systemd is the list of systemd units and dropin files for the package
	Systemd *SystemdConfiguration `yaml:"systemd,omitempty" json:"systemd,omitempty"`

	// Libs is the list of library files to be installed.
	// On linux this would typically be installed to /usr/lib/<package name>
	Libs map[string]ArtifactConfig `yaml:"libs,omitempty" json:"libs,omitempty"`

	// Links is the list of symlinks to be installed with the package
	// Links should only be used if the *package* should contain the link.
	// For making a container compatible with another image, use [PostInstall] in
	// the [ImageConfig].
	Links []ArtifactSymlinkConfig `yaml:"links,omitempty" json:"links,omitempty"`

	// Headers is a list of header files and/or folders to be installed.
	// On linux this would typically be installed to /usr/include/.
	Headers map[string]ArtifactConfig `yaml:"headers,omitempty" json:"headers,omitempty"`

	// Users is a list of users to add to the system when the package is installed.
	Users []AddUserConfig `yaml:"users,omitempty" json:"users,omitempty"`
	// Groups is a list of groups to add to the system when the package is installed.
	Groups []AddGroupConfig `yaml:"groups,omitempty" json:"groups,omitempty"`

	// DisableStrip is used to disable stripping of artifacts.
	DisableStrip bool `yaml:"disable_strip,omitempty" json:"disable_strip,omitempty"`

	// DisableAutoRequires is used to disable automatic dependency discovery for
	// the produced package.
	//
	// Some tooling, such as `rpmbuild`, will look at all artifacts and
	// automatically inject missing dependencies into the package metadata.
	// For instance, if you include a `.sh` script, rpmbuild with automatically
	// add `bash` as a dependency for the package.
	// It also does this for libraries being linked against.
	//
	// This is useful if you want to have more control over the dependencies
	// that are included in the package.
	// However, you must be careful to manually include all dependencies that are required.
	DisableAutoRequires bool `yaml:"disable_auto_requires,omitempty" json:"disable_auto_requires,omitempty"`

	// PackageFiles provides package-specific file listings that can be used when
	// files are installed via make install or similar commands rather than being
	// explicitly categorized into specific artifact types.
	// 
	// This is useful for projects that use make install or similar installation
	// mechanisms to place files in their intended locations and want to specify
	// which files should be included in the package for specific package formats.
	//
	// The key is the package format (e.g., "rpm", "deb") and the value is the
	// raw file listing content for that package format.
	//
	// For RPM packages, this would contain the content for the %files section.
	// Example:
	//   rpm: |
	//     %{_bindir}/myapp
	//     %{_includedir}/myapp.h
	//     %{_mandir}/man1/myapp.1*
	PackageFiles map[string]string `yaml:"package_files,omitempty" json:"package_files,omitempty"`
}

type ArtifactSymlinkConfig struct {
	// Source is the path that is being linked to
	// Example:
	//   If you want a symlink in /usr/bin/foo that is linking to /usr/bin/foo/foo
	//   then the `Source` is `/usr/bin/foo/foo`
	Source string `yaml:"source,omitempty" json:"source,omitempty"`
	// Dest is the path where the symlink will be installed
	Dest string `yaml:"dest,omitempty" json:"dest,omitempty"`
	// User is the user name that should own the symlink
	User string `yaml:"user,omitempty" json:"user,omitempty"`
	// Group is the group name that should own the symlink
	Group string `yaml:"group,omitempty" json:"group,omitempty"`
}

// CreateArtifactDirectories describes various directories that should be created on install.
// CreateArtifactDirectories represents different directory paths that are common to RPM systems.
type CreateArtifactDirectories struct {
	// Config is a list of directories the RPM should place under the system config directory (i.e. /etc)
	Config map[string]ArtifactDirConfig `yaml:"config,omitempty" json:"config,omitempty"`
	// State is a list of directories the RPM should place under the common directory for shared state and libs (i.e. /var/lib).
	State map[string]ArtifactDirConfig `yaml:"state,omitempty" json:"state,omitempty"`
}

func (d *CreateArtifactDirectories) GetConfig() map[string]ArtifactDirConfig {
	if d == nil {
		return nil
	}
	return maps.Clone(d.Config)
}

func (d *CreateArtifactDirectories) GetState() map[string]ArtifactDirConfig {
	if d == nil {
		return nil
	}
	return maps.Clone(d.State)
}

// ArtifactDirConfig contains information about the directory to be created
type ArtifactDirConfig struct {
	// Mode is used to set the file permission bits of the final created directory to the specified mode.
	// Mode is the octal permissions to set on the dir.
	Mode fs.FileMode `yaml:"mode,omitempty" json:"mode,omitempty"`
	// User is the user name that should own the directory
	User string `yaml:"user,omitempty" json:"user,omitempty"`
	// Group is the group name that should own the directory
	Group string `yaml:"group,omitempty" json:"group,omitempty"`
}

// ArtifactConfig is the configuration for a given artifact type.
// This is used to customize where an artifact will be placed when installed.
type ArtifactConfig struct {
	// Subpath is the subpath to use in the package for the artifact type.
	//
	// As an example, binaries are typically placed in /usr/bin when installed.
	// If you want to nest them in a subdirectory, you can specify it here.
	SubPath string `yaml:"subpath,omitempty" json:"subpath,omitempty"`
	// Name is file or dir name to use for the artifact in the package.
	// If empty, the file or dir name from the produced artifact will be used.
	Name string `yaml:"name,omitempty" json:"name,omitempty"`
	// Permissions is the file permissions to set on the artifact.
	// If not set, the default value will depend on the kind of artifact or the underlying artifact's already set permissions.
	Permissions fs.FileMode `yaml:"permissions,omitempty" json:"permissions,omitempty"`
	// User is the user name that should own the artifact
	User string `yaml:"user,omitempty" json:"user,omitempty"`
	// Group is the group name that should own the artifact
	Group string `yaml:"group,omitempty" json:"group,omitempty"`
}

func (a *ArtifactConfig) ResolveName(path string) string {
	if a.Name != "" {
		return a.Name
	}
	return filepath.Base(path)
}

// AddUserConfig is the configuration for adding a user to the system.
type AddUserConfig struct {
	// Name is the name of the user to add to the system.
	Name string `yaml:"name" json:"name"`
}

// AddGroupConfig is the configuration for adding a group to the system.
type AddGroupConfig struct {
	// Name is the name of the group to add to the system.
	Name string `yaml:"name" json:"name"`
}

// IsEmpty is used to determine if there are any artifacts to include in the package.
func (a *Artifacts) IsEmpty() bool {
	if len(a.Binaries) > 0 {
		return false
	}
	if len(a.Manpages) > 0 {
		return false
	}
	if a.Directories != nil && (len(a.Directories.Config) > 0 || len(a.Directories.State) > 0) {
		return false
	}
	if len(a.DataDirs) > 0 {
		return false
	}
	if len(a.ConfigFiles) > 0 {
		return false
	}

	if a.Systemd != nil &&
		(len(a.Systemd.Units) > 0 || len(a.Systemd.Dropins) > 0) {
		return false
	}

	if len(a.Docs) > 0 {
		return false
	}
	if len(a.Licenses) > 0 {
		return false
	}
	if len(a.Libs) > 0 {
		return false
	}
	if len(a.Links) > 0 {
		return false
	}
	if len(a.Headers) > 0 {
		return false
	}
	if len(a.PackageFiles) > 0 {
		return false
	}
	return true
}

func (a Artifacts) HasDocs() bool {
	if len(a.Docs) > 0 {
		return true
	}
	if len(a.Manpages) > 0 {
		return true
	}
	return false
}
