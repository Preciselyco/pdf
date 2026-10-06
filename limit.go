package pdf

import (
	"errors"
	"fmt"
	"runtime"
)

// ErrLimit is wrapped by every error the package raises because the input
// exceeds a safety limit, as opposed to being malformed. It is returned, or
// carried by a panic from methods that cannot return an error, so a recovered
// value can be tested with errors.Is.
var ErrLimit = errors.New("pdf: input exceeds a safety limit")

// limitf returns an error describing the limit that was exceeded, wrapping
// ErrLimit.
func limitf(format string, args ...any) error {
	return fmt.Errorf(format+": %w", append(args, ErrLimit)...)
}

// asError returns the value recovered from a panic as an error, keeping the
// chain of one that already is, so that errors.Is still sees ErrLimit.
func asError(x any) error {
	if err, ok := x.(error); ok {
		return err
	}
	return fmt.Errorf("%v", x)
}

// fatal reports whether a panic value recovered while reading must be raised
// again rather than taken for malformed input to skip: a runtime error is a
// bug, and a limit is no reason to drop part of a document quietly.
func fatal(e any) bool {
	if _, ok := e.(runtime.Error); ok {
		return true
	}
	return errors.Is(asError(e), ErrLimit)
}

// wrapf prefixes err with prefix, unless err is a limit, which is not a
// malformed file and keeps its own message.
func wrapf(prefix string, err error) error {
	if errors.Is(err, ErrLimit) {
		return err
	}
	return fmt.Errorf("%s: %w", prefix, err)
}
