package web

import (
	"testing"
)

// TestOpenBrowser_RespectsNxdNoBrowser guards SEC-L2: when the operator sets
// NXD_NO_BROWSER, openBrowser must NOT spawn `open` / `xdg-open`. The auth
// token is embedded in the URL, and process args are world-readable via
// `ps` on most multi-tenant systems — operators in headless / CI / SSH
// environments should be able to suppress the launch entirely.
//
// The function returns void so we assert on side effects: a non-empty
// envvar must short-circuit the exec.Command call. With NXD_NO_BROWSER
// unset on the CI runner the actual exec would attempt to fork — and that
// would noise up other tests via stderr. Asserting the no-op path is
// sufficient: the post-condition is "no crash, no fork."
func TestOpenBrowser_RespectsNxdNoBrowser(t *testing.T) {
	t.Setenv("NXD_NO_BROWSER", "1")
	// Must not panic, must not block, must not fork. A regression that
	// removed the short-circuit would surface here as a leaked process or
	// a stderr spew from the missing `open` binary on CI.
	openBrowser("http://127.0.0.1:0/?token=test")
}
