package orgdatacore

import (
	"fmt"
	"runtime/debug"
)

// Version variables - set at build time via -ldflags or read from build info.
// The default version follows semantic versioning (semver).
// Version 0.x.y indicates the library is not yet stable and breaking changes may occur.
var (
	Version      = "0.1.0"
	GitCommit    = "unknown"
	GitTreeState = "unknown"
	BuildDate    = "unknown"
)

type VersionInfo struct {
	Version      string `json:"version"`
	GitCommit    string `json:"git_commit"`
	GitTreeState string `json:"git_tree_state"`
	BuildDate    string `json:"build_date"`
	GoVersion    string `json:"go_version"`
	Compiler     string `json:"compiler"`
	Platform     string `json:"platform"`
}

// GetVersionInfo returns version information, enriching with runtime/debug info when available.
func GetVersionInfo() VersionInfo {
	info := VersionInfo{
		Version:      Version,
		GitCommit:    GitCommit,
		GitTreeState: GitTreeState,
		BuildDate:    BuildDate,
	}

	if buildInfo, ok := debug.ReadBuildInfo(); ok {
		info.GoVersion = buildInfo.GoVersion

		for _, setting := range buildInfo.Settings {
			switch setting.Key {
			case "vcs.revision":
				if info.GitCommit == "unknown" {
					info.GitCommit = setting.Value
				}
			case "vcs.modified":
				if info.GitTreeState == "unknown" {
					if setting.Value == "true" {
						info.GitTreeState = "dirty"
					} else {
						info.GitTreeState = "clean"
					}
				}
			case "GOOS":
				info.Platform = setting.Value
			case "GOARCH":
				if info.Platform != "" {
					info.Platform = info.Platform + "/" + setting.Value
				} else {
					info.Platform = setting.Value
				}
			case "-compiler":
				info.Compiler = setting.Value
			}
		}

		if (info.Version == "0.1.0" || info.Version == "dev") && buildInfo.Main.Version != "" && buildInfo.Main.Version != "(devel)" {
			info.Version = buildInfo.Main.Version
		}
	}

	return info
}

func (v VersionInfo) String() string {
	return fmt.Sprintf("orgdatacore %s (commit: %s, built: %s, go: %s)",
		v.Version, shortCommit(v.GitCommit), v.BuildDate, v.GoVersion)
}

func (v VersionInfo) Short() string {
	return v.Version
}

func shortCommit(commit string) string {
	if len(commit) > 7 {
		return commit[:7]
	}
	return commit
}

func GetLibraryVersion() string {
	return Version
}
