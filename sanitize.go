package wace

import (
	"regexp"
	"slices"
	"strings"

	"github.com/tilsor/ModSecIntl_wace_lib/waceapi"
)

const passwordRegex = `(?i)((new|old|form_|)(v)?password|clave|pass(|2))=([^*&\n]*)`

var (
	credentialRegex   = regexp.MustCompile(passwordRegex)
	credentialHeaders = []string{"authorization", "cookie", "set-cookie"}
)

// sanitizeCredentials replaces password-like fields in the request body and
// known credential headers (Authorization, Cookie, Set-Cookie) with "********".
func sanitizeCredentials(p waceapi.HTTPPayload) waceapi.HTTPPayload {
	p.RequestBody = string(credentialRegex.ReplaceAll([]byte(p.RequestBody), []byte("$1=********")))

	p.URI = string(credentialRegex.ReplaceAll([]byte(p.URI), []byte("$1=********")))

	for i := range p.RequestHeaders {
		if slices.Contains(credentialHeaders, strings.ToLower(p.RequestHeaders[i].Key)) {
			p.RequestHeaders[i] = waceapi.HTTPHeader{Key: p.RequestHeaders[i].Key, Value: "********"}
		}
	}

	for i := range p.ResponseHeaders {
		if slices.Contains(credentialHeaders, strings.ToLower(p.ResponseHeaders[i].Key)) {
			p.ResponseHeaders[i] = waceapi.HTTPHeader{Key: p.ResponseHeaders[i].Key, Value: "********"}
		}
	}

	return p
}

// setCredentialHeaders allows the user to change the list of headers to be sanitized.
func setCredentialHeaders(headers []string) {
	for i := range headers {
		headers[i] = strings.ToLower(headers[i])
	}
	credentialHeaders = headers
}
