//go:build !cgo

package index

import "errors"

// CanExtract is false in a build without the parsers: the server image is
// static and reads indexes, the operator image builds them.
const CanExtract = false

// Extract needs the parsers, which a cgo build carries.
func Extract(lang Lang, repo, path string, src []byte) (Extracted, error) {
	return Extracted{}, errors.New("index build needs a cgo build of vig-op")
}
