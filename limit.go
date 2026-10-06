package pdf

import (
	"errors"
	"fmt"
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
