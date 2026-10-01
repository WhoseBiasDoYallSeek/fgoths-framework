package assets

import (
	"net/url"
	"path"
	"strings"
)

var versions = map[string]string{}

// URL returns a static asset URL with its content fingerprint.
func URL(name string) string {
	name = assetPath(name)
	version := versions[name]
	assetURL := (&url.URL{Path: "/static/" + name}).EscapedPath()
	if version == "" {
		return assetURL
	}
	return assetURL + "?v=" + url.QueryEscape(version)
}

// IsVersioned reports whether version matches the generated content fingerprint.
func IsVersioned(name, version string) bool {
	return version != "" && versions[assetPath(name)] == version
}

func assetPath(name string) string {
	return strings.TrimPrefix(path.Clean("/"+name), "/")
}
