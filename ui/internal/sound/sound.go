//go:build !nosound

// Package sound plays event sounds from the system sound theme through
// gsound (a thin GObject layer over libcanberra). Playback is asynchronous
// and never fatal: a missing theme or sound server is logged and ignored.
//
// Build without the gsound dependency using -tags nosound; the Makefile
// does that automatically when pkg-config cannot find gsound.
package sound

/*
#cgo pkg-config: gsound
#include <gsound.h>

// gsound_context_play_simple is variadic; cgo cannot call it directly.
static gboolean malachi_play_event(GSoundContext *ctx, const char *id, const char *desc, GError **err) {
	return gsound_context_play_simple(ctx, NULL, err,
		GSOUND_ATTR_EVENT_ID, id,
		GSOUND_ATTR_EVENT_DESCRIPTION, desc,
		NULL);
}
*/
import "C"

import (
	"errors"
	"sync"
	"unsafe"
)

// Available reports whether sound support was compiled in.
const Available = true

var (
	once   sync.Once
	ctx    *C.GSoundContext
	ctxErr error
)

func context() (*C.GSoundContext, error) {
	once.Do(func() {
		var gerr *C.GError
		ctx = C.gsound_context_new(nil, &gerr)
		if gerr != nil {
			ctxErr = errors.New(C.GoString(gerr.message))
			C.g_error_free(gerr)
		}
	})
	return ctx, ctxErr
}

// Play starts the sound theme event eventID (XDG sound naming, e.g.
// "message-new-email"; the theme falls back to "message-new" and "message")
// and returns without waiting for it to finish. description is shown by
// accessibility tools.
func Play(eventID, description string) error {
	c, err := context()
	if err != nil {
		return err
	}
	cid := C.CString(eventID)
	defer C.free(unsafe.Pointer(cid))
	cdesc := C.CString(description)
	defer C.free(unsafe.Pointer(cdesc))

	var gerr *C.GError
	if C.malachi_play_event(c, cid, cdesc, &gerr) == 0 && gerr != nil {
		err := errors.New(C.GoString(gerr.message))
		C.g_error_free(gerr)
		return err
	}
	return nil
}
