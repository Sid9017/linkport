//go:build darwin && cgo

package main

/*
#cgo LDFLAGS: -framework Foundation
#include <stdlib.h>
char *linkportPreferredLanguage(void);
*/
import "C"

import "unsafe"

func systemLocale() string {
	locale := C.linkportPreferredLanguage()
	if locale == nil {
		return ""
	}
	defer C.free(unsafe.Pointer(locale))
	return C.GoString(locale)
}
