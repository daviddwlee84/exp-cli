package cli

import (
	"runtime/debug"
	"strconv"
	"strings"
)

const developmentVersion = "dev"

// Version is set by release builds through -ldflags. VersionFromBuild falls
// back to Go module and VCS build metadata for go install and local builds.
var Version = developmentVersion

// VersionFromBuild returns the most accurate version available in this build.
func VersionFromBuild() string {
	info, _ := debug.ReadBuildInfo()
	return resolveVersion(Version, info)
}

func resolveVersion(linked string, info *debug.BuildInfo) string {
	linked = strings.TrimSpace(linked)
	if linked != "" && linked != developmentVersion && linked != "(devel)" {
		return linked
	}

	if info != nil {
		moduleVersion := strings.TrimSpace(info.Main.Version)
		if moduleVersion != "" && moduleVersion != "(devel)" {
			return moduleVersion
		}

		var revision string
		modified := false
		for _, setting := range info.Settings {
			switch setting.Key {
			case "vcs.revision":
				revision = setting.Value
			case "vcs.modified":
				modified, _ = strconv.ParseBool(setting.Value)
			}
		}
		if revision != "" {
			if len(revision) > 12 {
				revision = revision[:12]
			}
			version := developmentVersion + "+" + revision
			if modified {
				version += ".dirty"
			}
			return version
		}
	}

	return developmentVersion
}
