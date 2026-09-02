//go:build nosound

package sound

import "errors"

// Available reports whether sound support was compiled in.
const Available = false

// ErrUnavailable is returned by Play in a build without gsound.
var ErrUnavailable = errors.New("built without sound support (nosound tag)")

// Play does nothing in a build without gsound.
func Play(eventID, description string) error { return ErrUnavailable }
