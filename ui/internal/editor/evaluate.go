package editor

/*
#cgo pkg-config: webkitgtk-6.0
#include <stdint.h>
#include <webkit/webkit.h>

// The Go binding of WebKitGTK 6.0 has no async starters, so
// webkit_web_view_evaluate_javascript is called through this fire-and-forget
// shim. The view is passed as an integer to keep unsafe.Pointer(uintptr)
// conversions out of Go (go vet's unsafeptr check).
static void malachi_eval(uintptr_t view, const char *script) {
	webkit_web_view_evaluate_javascript(WEBKIT_WEB_VIEW((gpointer)view), script, -1,
		NULL, NULL, NULL, NULL, NULL);
}
*/
import "C"

import (
	"unsafe"

	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
)

// eval runs script in the page's main world without waiting for a result.
// It is a no-op while enable-javascript is false (WebKit ignores the call),
// which editor.blp never allows. Only literals produced by jsString may be
// spliced into script.
func (e *Editor) eval(script string) {
	cs := C.CString(script)
	defer C.free(unsafe.Pointer(cs))
	C.malachi_eval(C.uintptr_t(coreglib.InternObject(e.WebView).Native()), cs)
}
