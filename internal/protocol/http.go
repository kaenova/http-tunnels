package protocol

import (
	"crypto/rand"
	"encoding/base64"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strings"
)

var hopByHopHeaders = map[string]struct{}{
	"Proxy-Connection":    {},
	"Keep-Alive":          {},
	"Proxy-Authenticate":  {},
	"Proxy-Authorization": {},
	"Te":                  {},
	"Trailer":             {},
	"Transfer-Encoding":   {},
}

func GenerateID(size int) string {
	buf := make([]byte, size)
	_, _ = rand.Read(buf)
	return base64.RawURLEncoding.EncodeToString(buf)
}

func CloneHeaders(headers http.Header) map[string][]string {
	if headers == nil {
		return nil
	}
	cloned := make(map[string][]string, len(headers))
	for key, values := range headers {
		if _, blocked := hopByHopHeaders[http.CanonicalHeaderKey(key)]; blocked {
			continue
		}
		copied := make([]string, len(values))
		copy(copied, values)
		cloned[key] = copied
	}
	return cloned
}

func ApplyHeaders(dst http.Header, src map[string][]string) {
	for key, values := range src {
		if _, blocked := hopByHopHeaders[http.CanonicalHeaderKey(key)]; blocked {
			continue
		}
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}

func MergeForwardedHeaders(headers map[string][]string, r *http.Request) map[string][]string {
	cloned := CloneHeaders(r.Header)
	if headers == nil {
		headers = cloned
	}
	if headers == nil {
		headers = make(map[string][]string)
	}
	if _, ok := headers["X-Forwarded-Host"]; !ok {
		headers["X-Forwarded-Host"] = []string{r.Host}
	}
	if _, ok := headers["X-Forwarded-Proto"]; !ok {
		proto := "http"
		if r.TLS != nil {
			proto = "https"
		}
		headers["X-Forwarded-Proto"] = []string{proto}
	}
	if remoteIP := strings.TrimSpace(r.RemoteAddr); remoteIP != "" {
		headers["X-Forwarded-For"] = append(headers["X-Forwarded-For"], remoteIP)
	}
	return headers
}

func BuildDestinationURL(base *url.URL, requestPath string) (*url.URL, error) {
	ref, err := url.Parse(requestPath)
	if err != nil {
		return nil, err
	}

	destination := *base
	basePath := strings.TrimSuffix(destination.Path, "/")
	requestURLPath := strings.TrimPrefix(ref.Path, "/")

	if basePath == "" {
		destination.Path = "/" + requestURLPath
	} else if requestURLPath == "" {
		destination.Path = basePath
	} else {
		destination.Path = path.Join(basePath, requestURLPath)
		if !strings.HasPrefix(destination.Path, "/") {
			destination.Path = "/" + destination.Path
		}
	}
	if strings.HasSuffix(ref.Path, "/") && !strings.HasSuffix(destination.Path, "/") {
		destination.Path += "/"
	}
	destination.RawQuery = ref.RawQuery
	destination.Fragment = ref.Fragment
	return &destination, nil
}

func NormalizeHost(host string) string {
	return strings.ToLower(strings.TrimSpace(host))
}

func IsTextContentType(contentType string) bool {
	if contentType == "" {
		return false
	}
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		mediaType = contentType
	}
	mediaType = strings.ToLower(strings.TrimSpace(mediaType))
	if strings.HasPrefix(mediaType, "text/") {
		return true
	}
	switch mediaType {
	case "application/json", "application/ld+json", "application/xml", "application/javascript", "application/x-www-form-urlencoded", "application/xhtml+xml", "application/problem+json", "application/problem+xml", "text/event-stream":
		return true
	default:
		return strings.HasSuffix(mediaType, "+json") || strings.HasSuffix(mediaType, "+xml")
	}
}
