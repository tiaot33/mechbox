//go:build !server

package main

/*
#cgo LDFLAGS: -framework CoreFoundation -framework Security
#include <stdlib.h>
#include <CoreFoundation/CoreFoundation.h>
#include <Security/Security.h>

static CFMutableDictionaryRef keychainQuery(const char *service, const char *account) {
	CFMutableDictionaryRef query = CFDictionaryCreateMutable(NULL, 0, &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
	CFStringRef s = CFStringCreateWithCString(NULL, service, kCFStringEncodingUTF8);
	CFStringRef a = CFStringCreateWithCString(NULL, account, kCFStringEncodingUTF8);
	CFDictionarySetValue(query, kSecClass, kSecClassGenericPassword);
	CFDictionarySetValue(query, kSecAttrService, s);
	CFDictionarySetValue(query, kSecAttrAccount, a);
	CFRelease(s);
	CFRelease(a);
	return query;
}

static OSStatus keychainGet(const char *service, const char *account, void **data, long *length) {
	CFMutableDictionaryRef query = keychainQuery(service, account);
	CFDictionarySetValue(query, kSecReturnData, kCFBooleanTrue);
	CFDictionarySetValue(query, kSecMatchLimit, kSecMatchLimitOne);
	CFTypeRef result = NULL;
	OSStatus status = SecItemCopyMatching(query, &result);
	CFRelease(query);
	if (status != errSecSuccess) {
		return status;
	}
	CFIndex n = CFDataGetLength((CFDataRef)result);
	*data = malloc(n > 0 ? n : 1);
	CFDataGetBytes((CFDataRef)result, CFRangeMake(0, n), (UInt8 *)*data);
	*length = n;
	CFRelease(result);
	return errSecSuccess;
}

static OSStatus keychainSet(const char *service, const char *account, const void *data, long length) {
	CFMutableDictionaryRef query = keychainQuery(service, account);
	CFDataRef value = CFDataCreate(NULL, (const UInt8 *)data, length);
	CFMutableDictionaryRef update = CFDictionaryCreateMutable(NULL, 0, &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
	CFDictionarySetValue(update, kSecValueData, value);
	OSStatus status = SecItemUpdate(query, update);
	if (status == errSecItemNotFound) {
		CFDictionarySetValue(query, kSecValueData, value);
		status = SecItemAdd(query, NULL);
	}
	CFRelease(update);
	CFRelease(value);
	CFRelease(query);
	return status;
}

static OSStatus keychainDelete(const char *service, const char *account) {
	CFMutableDictionaryRef query = keychainQuery(service, account);
	OSStatus status = SecItemDelete(query);
	CFRelease(query);
	return status;
}

static char *keychainMessage(OSStatus status) {
	CFStringRef message = SecCopyErrorMessageString(status, NULL);
	if (message == NULL) {
		return NULL;
	}
	CFIndex size = CFStringGetMaximumSizeForEncoding(CFStringGetLength(message), kCFStringEncodingUTF8) + 1;
	char *text = malloc(size);
	if (!CFStringGetCString(message, text, size, kCFStringEncodingUTF8)) {
		text[0] = 0;
	}
	CFRelease(message);
	return text;
}
*/
import "C"

import (
	"fmt"
	"unsafe"
)

func (k keychain) get() ([]byte, bool, error) {
	service, account := C.CString(k.service), C.CString(k.account)
	defer C.free(unsafe.Pointer(service))
	defer C.free(unsafe.Pointer(account))
	var data unsafe.Pointer
	var length C.long
	status := C.keychainGet(service, account, &data, &length)
	if status == C.errSecItemNotFound {
		return nil, false, nil
	}
	if status != C.errSecSuccess {
		return nil, false, keychainError("read", status)
	}
	defer C.free(data)
	return C.GoBytes(data, C.int(length)), true, nil
}

func (k keychain) set(value []byte) error {
	service, account := C.CString(k.service), C.CString(k.account)
	defer C.free(unsafe.Pointer(service))
	defer C.free(unsafe.Pointer(account))
	data := C.CBytes(value)
	defer C.free(data)
	if status := C.keychainSet(service, account, data, C.long(len(value))); status != C.errSecSuccess {
		return keychainError("write", status)
	}
	return nil
}

func (k keychain) delete() error {
	service, account := C.CString(k.service), C.CString(k.account)
	defer C.free(unsafe.Pointer(service))
	defer C.free(unsafe.Pointer(account))
	if status := C.keychainDelete(service, account); status != C.errSecSuccess && status != C.errSecItemNotFound {
		return keychainError("delete", status)
	}
	return nil
}

func keychainError(op string, status C.OSStatus) error {
	text := C.keychainMessage(status)
	if text == nil {
		return fmt.Errorf("%s keychain item: OSStatus %d", op, int(status))
	}
	defer C.free(unsafe.Pointer(text))
	return fmt.Errorf("%s keychain item: %s (OSStatus %d)", op, C.GoString(text), int(status))
}
