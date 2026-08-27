// Package deviceid carries the identity a connection was admitted under, from
// whatever checked the credential to whatever serves the connection.
//
// It is a leaf package because the two ends must not depend on each other. The
// component that admits knows what a credential is; the backend that serves the
// connection knows only that somebody named it, or that nobody did. Passing the
// identity on the request keeps the second from having to learn the first.
package deviceid

import (
	"context"
	"net/http"
)

// contextKey is unexported so nothing outside this package can write the
// identity by hand: it is set by [With] or it is absent.
type contextKey struct{}

// With returns r carrying id as the identity it was admitted under.
//
// An empty id is left off the request rather than stored, so [Of] reports the
// same thing for "admitted under no name" as it does for a request nothing
// admitted at all. Neither one names a device.
func With(r *http.Request, id string) *http.Request {
	if r == nil || id == "" {
		return r
	}
	return r.WithContext(context.WithValue(r.Context(), contextKey{}, id))
}

// Of reports the identity a request was admitted under, empty when nothing
// admitted it.
//
// Empty is not a failure: a backend reached directly, with nothing checking
// credentials in front of it, serves every connection under no identity. What
// that means is the backend's to decide.
func Of(r *http.Request) string {
	if r == nil {
		return ""
	}
	id, _ := r.Context().Value(contextKey{}).(string)
	return id
}
