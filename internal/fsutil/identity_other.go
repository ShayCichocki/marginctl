//go:build !unix

package fsutil

import "os"

// FileIdentity is unavailable on this platform; canonical paths are used as
// the identity fallback.
type FileIdentity struct {
	Valid bool
}

func fileIdentity(os.FileInfo) FileIdentity { return FileIdentity{} }
