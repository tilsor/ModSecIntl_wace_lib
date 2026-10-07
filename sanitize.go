package wace

import (
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/tilsor/ModSecIntl_wace_lib/waceapi"
)

const credentialMask = "********"

// credentialFields are the field names, matched case-insensitively right
// before a '=', whose value is masked. Prefixed names such as newpassword
// or form_password are covered because only the suffix is checked.
var credentialFields = []string{"password", "pass", "pass2", "clave"}

// sanitizeCredentials returns a copy of p where password-like fields in the
// URI and request body, and the headers named in credentialHeaders, are
// replaced with "********".
func sanitizeCredentials(p waceapi.HTTPPayload, credentialHeaders []string) waceapi.HTTPPayload {
	p.URI = sanitizeFields(p.URI)
	p.RequestBody = sanitizeFields(p.RequestBody)

	p.RequestHeaders = sanitizeHeaders(p.RequestHeaders, credentialHeaders)
	p.ResponseHeaders = sanitizeHeaders(p.ResponseHeaders, credentialHeaders)

	return p
}

// sanitizeFields masks the value of every credential field in s, up to
// the next '*', '&' or newline. It returns s itself when nothing matches.
func sanitizeFields(s string) string {
	var sb strings.Builder
	copied := 0
	for i := 0; ; {
		j := strings.IndexByte(s[i:], '=')
		if j < 0 {
			break
		}
		eq := i + j
		i = eq + 1
		if !isCredentialField(s[:eq]) {
			continue
		}
		if sb.Len() == 0 {
			sb.Grow(len(s))
		}
		sb.WriteString(s[copied:i])
		sb.WriteString(credentialMask)
		if k := strings.IndexAny(s[i:], "*&\n"); k >= 0 {
			i += k
		} else {
			i = len(s)
		}
		copied = i
	}
	if copied == 0 {
		return s
	}
	sb.WriteString(s[copied:])
	return sb.String()
}

// isCredentialField reports whether s ends with a credential field name.
func isCredentialField(s string) bool {
	for _, f := range credentialFields {
		if hasFoldSuffix(s, f) {
			return true
		}
	}
	return false
}

// hasFoldSuffix reports whether s ends with the ASCII string suffix under
// Unicode simple case folding.
func hasFoldSuffix(s, suffix string) bool {
	for i := len(suffix) - 1; i >= 0; i-- {
		r, size := utf8.DecodeLastRuneInString(s)
		if size == 0 || !foldEq(r, rune(suffix[i])) {
			return false
		}
		s = s[:len(s)-size]
	}
	return true
}

// foldEq reports whether r equals the lowercase ASCII rune c under simple
// case folding.
func foldEq(r, c rune) bool {
	if r == c {
		return true
	}
	if r < utf8.RuneSelf {
		return 'A' <= r && r <= 'Z' && r+'a'-'A' == c
	}
	for f := unicode.SimpleFold(c); f != c; f = unicode.SimpleFold(f) {
		if f == r {
			return true
		}
	}
	return false
}

// sanitizeHeaders returns h with the value of every header named in names
// masked. h is cloned on the first match and returned as is if none.
func sanitizeHeaders(h []waceapi.HTTPHeader, names []string) []waceapi.HTTPHeader {
	var out []waceapi.HTTPHeader
	for i := range h {
		if !slices.ContainsFunc(names, func(n string) bool { return strings.EqualFold(h[i].Key, n) }) {
			continue
		}
		if out == nil {
			out = slices.Clone(h)
		}
		out[i].Value = credentialMask
	}
	if out == nil {
		return h
	}
	return out
}
