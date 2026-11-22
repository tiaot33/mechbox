//go:build darwin

package proxy

/*
#cgo LDFLAGS: -framework CoreFoundation -framework SystemConfiguration
#include <CoreFoundation/CoreFoundation.h>
#include <SystemConfiguration/SystemConfiguration.h>
#include <stdlib.h>

typedef struct {
	int http_enable;
	char *http_host;
	int http_port;
	int https_enable;
	char *https_host;
	int https_port;
	int socks_enable;
	char *socks_host;
	int socks_port;
	int exclude_simple;
	char **exceptions;
	int exception_count;
} darwin_proxy_info;

static int dict_int(CFDictionaryRef dict, CFStringRef key) {
	CFNumberRef num = (CFNumberRef)CFDictionaryGetValue(dict, key);
	if (!num || CFGetTypeID(num) != CFNumberGetTypeID()) {
		return 0;
	}
	int val = 0;
	if (!CFNumberGetValue(num, kCFNumberIntType, &val)) {
		return 0;
	}
	return val;
}

static char *cfstring_to_cstr(CFStringRef str) {
	if (!str || CFGetTypeID(str) != CFStringGetTypeID()) {
		return NULL;
	}
	CFIndex len = CFStringGetLength(str);
	CFIndex maxSize = CFStringGetMaximumSizeForEncoding(len, kCFStringEncodingUTF8) + 1;
	char *buf = (char *)malloc((size_t)maxSize);
	if (!buf) {
		return NULL;
	}
	if (!CFStringGetCString(str, buf, maxSize, kCFStringEncodingUTF8)) {
		free(buf);
		return NULL;
	}
	return buf;
}

static char *dict_string(CFDictionaryRef dict, CFStringRef key) {
	return cfstring_to_cstr((CFStringRef)CFDictionaryGetValue(dict, key));
}

static darwin_proxy_info read_darwin_proxy(void) {
	darwin_proxy_info info = {0};
	CFDictionaryRef dict = SCDynamicStoreCopyProxies(NULL);
	if (!dict) {
		return info;
	}
	info.http_enable = dict_int(dict, kSCPropNetProxiesHTTPEnable);
	if (info.http_enable) {
		info.http_host = dict_string(dict, kSCPropNetProxiesHTTPProxy);
		info.http_port = dict_int(dict, kSCPropNetProxiesHTTPPort);
	}
	info.https_enable = dict_int(dict, kSCPropNetProxiesHTTPSEnable);
	if (info.https_enable) {
		info.https_host = dict_string(dict, kSCPropNetProxiesHTTPSProxy);
		info.https_port = dict_int(dict, kSCPropNetProxiesHTTPSPort);
	}
	info.socks_enable = dict_int(dict, kSCPropNetProxiesSOCKSEnable);
	if (info.socks_enable) {
		info.socks_host = dict_string(dict, kSCPropNetProxiesSOCKSProxy);
		info.socks_port = dict_int(dict, kSCPropNetProxiesSOCKSPort);
	}
	info.exclude_simple = dict_int(dict, kSCPropNetProxiesExcludeSimpleHostnames);
	CFArrayRef arr = (CFArrayRef)CFDictionaryGetValue(dict, kSCPropNetProxiesExceptionsList);
	if (arr && CFGetTypeID(arr) == CFArrayGetTypeID()) {
		CFIndex count = CFArrayGetCount(arr);
		if (count > 0) {
			info.exceptions = (char **)calloc((size_t)count, sizeof(char *));
			if (info.exceptions) {
				int n = 0;
				for (CFIndex i = 0; i < count; i++) {
					char *item = cfstring_to_cstr((CFStringRef)CFArrayGetValueAtIndex(arr, i));
					if (item) {
						info.exceptions[n++] = item;
					}
				}
				info.exception_count = n;
			}
		}
	}
	CFRelease(dict);
	return info;
}

static void free_darwin_proxy(darwin_proxy_info *info) {
	free(info->http_host);
	free(info->https_host);
	free(info->socks_host);
	if (info->exceptions) {
		for (int i = 0; i < info->exception_count; i++) {
			free(info->exceptions[i]);
		}
		free(info->exceptions);
	}
}
*/
import "C"

import "unsafe"

func readSystemProxy() SystemConfig {
	info := C.read_darwin_proxy()
	defer C.free_darwin_proxy(&info)
	var cfg SystemConfig
	if info.http_enable != 0 && info.http_host != nil {
		cfg.HTTP = buildHostPortURL("http", C.GoString(info.http_host), int(info.http_port))
	}
	if info.https_enable != 0 && info.https_host != nil {
		cfg.HTTPS = buildHostPortURL("http", C.GoString(info.https_host), int(info.https_port))
	}
	if info.socks_enable != 0 && info.socks_host != nil {
		cfg.SOCKS = buildHostPortURL("socks5", C.GoString(info.socks_host), int(info.socks_port))
	}
	if info.exception_count > 0 && info.exceptions != nil {
		items := unsafe.Slice(info.exceptions, int(info.exception_count))
		cfg.Bypass = make([]string, 0, len(items)+1)
		for _, item := range items {
			if item != nil {
				cfg.Bypass = append(cfg.Bypass, C.GoString(item))
			}
		}
	}
	if info.exclude_simple != 0 {
		cfg.Bypass = append(cfg.Bypass, "<local>")
	}
	return cfg
}
