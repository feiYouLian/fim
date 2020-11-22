package peer

import (
	"unsafe"
)

// BytesToString BytesToString
func BytesToString(b []byte) string {
	return *(*string)(unsafe.Pointer(&b))
}

// StringToBytes StringToBytes
func StringToBytes(s string) []byte {
	return *(*[]byte)(unsafe.Pointer(&s))
}
