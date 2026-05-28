package wace

import (
	"reflect"
	"testing"

	"github.com/tilsor/ModSecIntl_wace_lib/waceapi"
)

func TestSanitizeCredentials(t *testing.T) {
	tests := []struct {
		name  string
		input waceapi.HTTPPayload
		want  waceapi.HTTPPayload
	}{
		// --- Body: password field variants ---
		{
			name:  "password= is sanitized",
			input: waceapi.HTTPPayload{RequestBody: "user=john&password=secret"},
			want:  waceapi.HTTPPayload{RequestBody: "user=john&password=********"},
		},
		{
			name:  "newpassword= is sanitized",
			input: waceapi.HTTPPayload{RequestBody: "newpassword=newsecret"},
			want:  waceapi.HTTPPayload{RequestBody: "newpassword=********"},
		},
		{
			name:  "oldpassword= is sanitized",
			input: waceapi.HTTPPayload{RequestBody: "oldpassword=oldsecret"},
			want:  waceapi.HTTPPayload{RequestBody: "oldpassword=********"},
		},
		{
			name:  "vpassword= is sanitized",
			input: waceapi.HTTPPayload{RequestBody: "vpassword=vsecret"},
			want:  waceapi.HTTPPayload{RequestBody: "vpassword=********"},
		},
		{
			name:  "form_password= is sanitized",
			input: waceapi.HTTPPayload{RequestBody: "form_password=formsecret"},
			want:  waceapi.HTTPPayload{RequestBody: "form_password=********"},
		},
		{
			name:  "clave= is sanitized",
			input: waceapi.HTTPPayload{RequestBody: "clave=mysecret"},
			want:  waceapi.HTTPPayload{RequestBody: "clave=********"},
		},
		{
			name:  "pass= is sanitized",
			input: waceapi.HTTPPayload{RequestBody: "pass=mypass"},
			want:  waceapi.HTTPPayload{RequestBody: "pass=********"},
		},
		{
			name:  "pass2= is sanitized",
			input: waceapi.HTTPPayload{RequestBody: "pass2=mypass"},
			want:  waceapi.HTTPPayload{RequestBody: "pass2=********"},
		},
		// --- Body: multiple and edge cases ---
		{
			name:  "multiple credential fields are all sanitized",
			input: waceapi.HTTPPayload{RequestBody: "password=first&pass2=second"},
			want:  waceapi.HTTPPayload{RequestBody: "password=********&pass2=********"},
		},
		{
			name:  "empty password value is sanitized",
			input: waceapi.HTTPPayload{RequestBody: "password="},
			want:  waceapi.HTTPPayload{RequestBody: "password=********"},
		},
		{
			name:  "non-credential fields are unchanged",
			input: waceapi.HTTPPayload{RequestBody: "user=john&email=john@example.com"},
			want:  waceapi.HTTPPayload{RequestBody: "user=john&email=john@example.com"},
		},
		{
			name:  "empty body is unchanged",
			input: waceapi.HTTPPayload{RequestBody: ""},
			want:  waceapi.HTTPPayload{RequestBody: ""},
		},
		// --- Headers ---
		{
			name: "Authorization header is sanitized",
			input: waceapi.HTTPPayload{RequestHeaders: []waceapi.HTTPHeader{
				{Key: "Authorization", Value: "Bearer token123"},
			}},
			want: waceapi.HTTPPayload{RequestHeaders: []waceapi.HTTPHeader{
				{Key: "Authorization", Value: "********"},
			}},
		},
		{
			name: "Cookie header is sanitized",
			input: waceapi.HTTPPayload{RequestHeaders: []waceapi.HTTPHeader{
				{Key: "Cookie", Value: "session=abc123"},
			}},
			want: waceapi.HTTPPayload{RequestHeaders: []waceapi.HTTPHeader{
				{Key: "Cookie", Value: "********"},
			}},
		},
		{
			name: "Set-Cookie header is sanitized",
			input: waceapi.HTTPPayload{ResponseHeaders: []waceapi.HTTPHeader{
				{Key: "Set-Cookie", Value: "session=abc; Path=/"},
			}},
			want: waceapi.HTTPPayload{ResponseHeaders: []waceapi.HTTPHeader{
				{Key: "Set-Cookie", Value: "********"},
			}},
		},
		{
			name: "header matching is case-insensitive",
			input: waceapi.HTTPPayload{RequestHeaders: []waceapi.HTTPHeader{
				{Key: "AUTHORIZATION", Value: "Bearer token"},
			}},
			want: waceapi.HTTPPayload{RequestHeaders: []waceapi.HTTPHeader{
				{Key: "AUTHORIZATION", Value: "********"},
			}},
		},
		{
			name: "header key is preserved after sanitization",
			input: waceapi.HTTPPayload{RequestHeaders: []waceapi.HTTPHeader{
				{Key: "Authorization", Value: "secret"},
			}},
			want: waceapi.HTTPPayload{RequestHeaders: []waceapi.HTTPHeader{
				{Key: "Authorization", Value: "********"},
			}},
		},
		{
			name: "non-credential headers are unchanged",
			input: waceapi.HTTPPayload{RequestHeaders: []waceapi.HTTPHeader{
				{Key: "Content-Type", Value: "application/json"},
				{Key: "User-Agent", Value: "TestAgent/1.0"},
			}},
			want: waceapi.HTTPPayload{RequestHeaders: []waceapi.HTTPHeader{
				{Key: "Content-Type", Value: "application/json"},
				{Key: "User-Agent", Value: "TestAgent/1.0"},
			}},
		},
		{
			name: "multiple credential headers are all sanitized",
			input: waceapi.HTTPPayload{
				RequestHeaders: []waceapi.HTTPHeader{
					{Key: "Authorization", Value: "Bearer tok"},
					{Key: "Cookie", Value: "s=xyz"},
				},
				ResponseHeaders: []waceapi.HTTPHeader{
					{Key: "Set-Cookie", Value: "s=xyz; Path=/"},
				}},
			want: waceapi.HTTPPayload{
				RequestHeaders: []waceapi.HTTPHeader{
					{Key: "Authorization", Value: "********"},
					{Key: "Cookie", Value: "********"},
				},
				ResponseHeaders: []waceapi.HTTPHeader{
					{Key: "Set-Cookie", Value: "********"},
				}},
		},
		// --- Combined ---
		{
			name: "body and headers are both sanitized",
			input: waceapi.HTTPPayload{
				RequestBody: "user=john&password=secret",
				RequestHeaders: []waceapi.HTTPHeader{
					{Key: "Authorization", Value: "Bearer token"},
					{Key: "Content-Type", Value: "application/json"},
				},
			},
			want: waceapi.HTTPPayload{
				RequestBody: "user=john&password=********",
				RequestHeaders: []waceapi.HTTPHeader{
					{Key: "Authorization", Value: "********"},
					{Key: "Content-Type", Value: "application/json"},
				},
			},
		},
		{
			name:  "payload with no sensitive data is returned unchanged",
			input: waceapi.HTTPPayload{URI: "/health", Method: "GET"},
			want:  waceapi.HTTPPayload{URI: "/health", Method: "GET"},
		},
		// --- Case-insensitive body matching ---
		{
			name:  "Password= (mixed case) is sanitized",
			input: waceapi.HTTPPayload{RequestBody: "Password=Secret"},
			want:  waceapi.HTTPPayload{RequestBody: "Password=********"},
		},
		{
			name:  "PASSWORD= (uppercase) is sanitized",
			input: waceapi.HTTPPayload{RequestBody: "PASSWORD=secret"},
			want:  waceapi.HTTPPayload{RequestBody: "PASSWORD=********"},
		},
		{
			name:  "CLAVE= (uppercase) is sanitized",
			input: waceapi.HTTPPayload{RequestBody: "CLAVE=mysecret"},
			want:  waceapi.HTTPPayload{RequestBody: "CLAVE=********"},
		},
		// --- URI sanitization ---
		{
			name:  "password in URI query string is sanitized",
			input: waceapi.HTTPPayload{URI: "/login?user=john&password=secret"},
			want:  waceapi.HTTPPayload{URI: "/login?user=john&password=********"},
		},
		{
			name:  "URI with no credentials is unchanged",
			input: waceapi.HTTPPayload{URI: "/api/v1/resource?filter=active"},
			want:  waceapi.HTTPPayload{URI: "/api/v1/resource?filter=active"},
		},
		{
			name:  "URI and body are both sanitized",
			input: waceapi.HTTPPayload{URI: "/login?pass=abc", RequestBody: "password=xyz"},
			want:  waceapi.HTTPPayload{URI: "/login?pass=********", RequestBody: "password=********"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeCredentials(tt.input)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("SanitizeCredentials() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestSetCredentialHeaders(t *testing.T) {
	original := credentialHeaders
	defer func() { credentialHeaders = original }()

	tests := []struct {
		name    string
		headers []string
		input   waceapi.HTTPPayload
		want    waceapi.HTTPPayload
	}{
		{
			name:    "custom header replaces defaults",
			headers: []string{"X-Api-Key"},
			input: waceapi.HTTPPayload{RequestHeaders: []waceapi.HTTPHeader{
				{Key: "X-Api-Key", Value: "secret"},
				{Key: "Authorization", Value: "Bearer token"},
			}},
			want: waceapi.HTTPPayload{RequestHeaders: []waceapi.HTTPHeader{
				{Key: "X-Api-Key", Value: "********"},
				{Key: "Authorization", Value: "Bearer token"},
			}},
		},
		{
			name:    "input header name casing is normalized before matching",
			headers: []string{"X-API-KEY"},
			input: waceapi.HTTPPayload{RequestHeaders: []waceapi.HTTPHeader{
				{Key: "x-api-key", Value: "secret"},
			}},
			want: waceapi.HTTPPayload{RequestHeaders: []waceapi.HTTPHeader{
				{Key: "x-api-key", Value: "********"},
			}},
		},
		{
			name:    "empty list means no headers are sanitized",
			headers: []string{},
			input: waceapi.HTTPPayload{
				RequestHeaders: []waceapi.HTTPHeader{
					{Key: "Authorization", Value: "Bearer token"},
					{Key: "Cookie", Value: "session=abc"},
				},
				ResponseHeaders: []waceapi.HTTPHeader{
					{Key: "Set-Cookie", Value: "s=xyz; Path=/"},
				},
			},
			want: waceapi.HTTPPayload{
				RequestHeaders: []waceapi.HTTPHeader{
					{Key: "Authorization", Value: "Bearer token"},
					{Key: "Cookie", Value: "session=abc"},
				},
				ResponseHeaders: []waceapi.HTTPHeader{
					{Key: "Set-Cookie", Value: "s=xyz; Path=/"},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			SetCredentialHeaders(tt.headers)
			got := sanitizeCredentials(tt.input)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("after SetCredentialHeaders(%v): sanitizeCredentials() = %+v, want %+v",
					tt.headers, got, tt.want)
			}
		})
	}
}
