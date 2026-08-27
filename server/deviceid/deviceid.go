// Package deviceid carries the identity a connection was admitted under, from
// whatever checked the credential to whatever serves the connection.
//
// A leaf package so the two ends need not depend on each other: the component
// that admits knows what a credential is, and the backend that serves the
// connection knows only whether somebody named it.
//
// It sits under server because it is request plumbing: it carries a value on an
// http.Request between what gates one and what serves it. Nothing here persists
// a credential, which is what secure/pairing does.
package deviceid

import (
	"context"
	"net/http"
)

// contextKey is unexported, so the identity is set by [With] or absent.
type contextKey struct{}

// With returns r carrying id as the identity it was admitted under.
//
// An empty id is not stored: "admitted under no name" and "nothing admitted
// this" both read back as empty, and neither names a device.
func With(r *http.Request, id string) *http.Request {
	if r == nil || id == "" {
		return r
	}
	return r.WithContext(context.WithValue(r.Context(), contextKey{}, id))
}

// Of reports the identity a request was admitted under, empty when nothing
// admitted it.
//
// Empty is not a failure. A backend mounted with no credential check in front
// of it serves every connection under no identity, and what that means is the
// backend's to decide.
func Of(r *http.Request) string {
	if r == nil {
		return ""
	}
	id, _ := r.Context().Value(contextKey{}).(string)
	return id
}
