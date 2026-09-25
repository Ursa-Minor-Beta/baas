// Package doctools marks failures caused by a missing document toolchain, so
// handlers can tell "not installed in this build" apart from a broken request.
package doctools

import "errors"

// ErrUnavailable matches, via errors.Is, every error built by Unavailable.
var ErrUnavailable = errors.New("document toolchain unavailable")

const hint = "document conversion is unavailable because the document toolchain was not found. " +
	"In Docker, rebuild the base image with --build-arg DOCUMENT_TOOLS=on; " +
	"running from source, install it locally (see the README)"

type unavailableError struct {
	reason error
}

// Unavailable wraps the lookup failure that disabled a converter. The result
// matches ErrUnavailable and still unwraps to reason.
func Unavailable(reason error) error {
	return &unavailableError{reason: reason}
}

func (e *unavailableError) Error() string {
	return hint + ": " + e.reason.Error()
}

func (e *unavailableError) Unwrap() []error {
	return []error{ErrUnavailable, e.reason}
}
