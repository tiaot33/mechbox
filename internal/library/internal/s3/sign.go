package s3

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"maps"
	"net/http"
	"slices"
	"strings"
	"time"
)

func uriEncode(value string, preserveSlash bool) string {
	const hexadecimal = "0123456789ABCDEF"
	var encoded strings.Builder
	for i := range len(value) {
		character := value[i]
		if character >= 'A' && character <= 'Z' || character >= 'a' && character <= 'z' || character >= '0' && character <= '9' || strings.ContainsRune("-._~", rune(character)) || preserveSlash && character == '/' {
			encoded.WriteByte(character)
			continue
		}
		encoded.WriteByte('%')
		encoded.WriteByte(hexadecimal[character>>4])
		encoded.WriteByte(hexadecimal[character&15])
	}
	return encoded.String()
}

func sign(req *http.Request, payload []byte, cfg config, now time.Time) {
	payloadSum := sha256.Sum256(payload)
	payloadHash := hex.EncodeToString(payloadSum[:])
	date := now.Format("20060102")
	amzDate := now.Format("20060102T150405Z")
	req.Header.Set("x-amz-content-sha256", payloadHash)
	req.Header.Set("x-amz-date", amzDate)
	if cfg.sessionToken != "" {
		req.Header.Set("x-amz-security-token", cfg.sessionToken)
	}
	values := map[string]string{"host": req.URL.Host}
	for name := range req.Header {
		if name = strings.ToLower(name); strings.HasPrefix(name, "x-amz-") {
			values[name] = strings.TrimSpace(req.Header.Get(name))
		}
	}
	names := slices.Sorted(maps.Keys(values))
	var headers strings.Builder
	for _, name := range names {
		headers.WriteString(name + ":" + values[name] + "\n")
	}
	signedHeaders := strings.Join(names, ";")
	canonical := req.Method + "\n" + req.URL.EscapedPath() + "\n" + req.URL.RawQuery + "\n" + headers.String() + "\n" + signedHeaders + "\n" + payloadHash
	canonicalSum := sha256.Sum256([]byte(canonical))
	scope := date + "/" + cfg.region + "/s3/aws4_request"
	toSign := "AWS4-HMAC-SHA256\n" + amzDate + "\n" + scope + "\n" + hex.EncodeToString(canonicalSum[:])
	kDate := hmacSHA256([]byte("AWS4"+cfg.secretKey), date)
	kRegion := hmacSHA256(kDate, cfg.region)
	kService := hmacSHA256(kRegion, "s3")
	kSigning := hmacSHA256(kService, "aws4_request")
	signature := hex.EncodeToString(hmacSHA256(kSigning, toSign))
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential="+cfg.accessKey+"/"+scope+", SignedHeaders="+signedHeaders+", Signature="+signature)
}

func hmacSHA256(key []byte, value string) []byte {
	h := hmac.New(sha256.New, key)
	_, _ = h.Write([]byte(value))
	return h.Sum(nil)
}
