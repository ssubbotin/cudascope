// Package buildinfo says which build of cudascope is running.
//
// The release build stamps the three variables below with -ldflags -X (see
// the Dockerfile and the Makefile). The Docker build context leaves .git out,
// so Go cannot record the commit there on its own.
package buildinfo

import "runtime/debug"

// Stamped at build time. Version is `git describe --tags --always`: a tag
// such as v0.1.0 on a release, v0.1.0-3-g1a2b3c4 on a later commit.
var (
	Version   string
	Revision  string
	BuildTime string // RFC 3339, UTC
)

// Info is the build as the API reports it.
type Info struct {
	Version  string `json:"version"`
	Revision string `json:"revision,omitempty"`
	BuiltAt  string `json:"built_at,omitempty"`
}

// Get returns the running build.
func Get() Info {
	bi, _ := debug.ReadBuildInfo()
	return resolve(Version, Revision, BuildTime, bi)
}

// resolve prefers what the build stamped, then the commit Go recorded when
// it built inside a checkout, and calls anything else dev. The time Go
// records is the commit's, so it never stands in for the build time.
func resolve(version, revision, builtAt string, bi *debug.BuildInfo) Info {
	info := Info{Version: version, Revision: revision, BuiltAt: builtAt}
	if info.Revision == "" && bi != nil {
		for _, s := range bi.Settings {
			if s.Key == "vcs.revision" {
				info.Revision = s.Value
			}
		}
	}
	if info.Version == "" {
		info.Version = "dev"
	}
	return info
}
