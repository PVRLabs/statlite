// Package urlshape contains URL structure checks shared by configuration and
// explicit endpoint inspection. Callers retain their own endpoint semantics.
package urlshape

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

type ErrorKind string

const (
	KindSyntax   ErrorKind = "syntax"
	KindScheme   ErrorKind = "scheme"
	KindHost     ErrorKind = "host"
	KindPort     ErrorKind = "port"
	KindFragment ErrorKind = "fragment"
)

// Error reports a structural URL failure without making callers inspect text.
type Error struct {
	Kind    ErrorKind
	Message string
}

func (e *Error) Error() string { return e.Message }

func newError(kind ErrorKind, message string) *Error {
	return &Error{Kind: kind, Message: message}
}

// ParseHTTP parses an absolute hierarchical HTTP URL with a non-empty host.
func ParseHTTP(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		// url.Parse errors can quote the original URL, including userinfo. Keep
		// this shared error safe for callers to report without sanitizing.
		return nil, newError(KindSyntax, "invalid URL syntax")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, newError(KindScheme, fmt.Sprintf("unsupported URL scheme %q; use http or https", u.Scheme))
	}
	if u.Host == "" || u.Hostname() == "" {
		return nil, newError(KindHost, "must include a host")
	}
	if port := u.Port(); port != "" {
		value, err := strconv.Atoi(port)
		if err != nil || value < 1 || value > 65535 {
			return nil, newError(KindPort, "port must be a number from 1 through 65535")
		}
	}
	if strings.Contains(raw, "#") {
		return nil, newError(KindFragment, "must not contain a fragment")
	}
	return u, nil
}
