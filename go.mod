module github.com/tzone85/nexus-dispatch

go 1.26.1

// toolchain pins the minimum Go toolchain so that builds (local and CI)
// pull a patch level that clears the 1.26.x stdlib CVEs surfaced by
// govulncheck. go1.26.9 clears the latest batch of CALLED standard-library
// vulnerabilities (GO-2026-6603/6605/6607/6608/6609/6610/6611/6612/6613/6617
// across net/http, net/textproto and crypto/tls — all "Fixed in go1.26.9")
// on top of the earlier net/http, encoding/xml, encoding/asn1 and crypto/tls
// batches cleared by go1.26.6 and the ECH leak fixed in go1.26.5. With
// GOTOOLCHAIN=auto (default), older local installs will fetch 1.26.9 on first
// build instead of compiling against a vulnerable stdlib. The called
// golang.org/x/text CVE (GO-2026-6629) is cleared by the v0.41.0 bump below.
toolchain go1.26.9

require (
	github.com/charmbracelet/bubbletea v1.3.10
	github.com/charmbracelet/lipgloss v1.1.0
	github.com/coder/websocket v1.8.15
	github.com/jackc/pgx/v5 v5.9.2
	github.com/mattn/go-sqlite3 v1.14.34
	github.com/oklog/ulid/v2 v2.1.1
	github.com/spf13/cobra v1.10.2
	golang.org/x/sys v0.36.0
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/aymanbagabas/go-osc52/v2 v2.0.1 // indirect
	github.com/charmbracelet/colorprofile v0.2.3-0.20250311203215-f60798e515dc // indirect
	github.com/charmbracelet/x/ansi v0.10.1 // indirect
	github.com/charmbracelet/x/cellbuf v0.0.13-0.20250311204145-2c3ea96c31dd // indirect
	github.com/charmbracelet/x/term v0.2.1 // indirect
	github.com/erikgeiser/coninput v0.0.0-20211004153227-1c3628e74d0f // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/kr/text v0.2.0 // indirect
	github.com/lucasb-eyer/go-colorful v1.2.0 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/mattn/go-localereader v0.0.1 // indirect
	github.com/mattn/go-runewidth v0.0.16 // indirect
	github.com/muesli/ansi v0.0.0-20230316100256-276c6243b2f6 // indirect
	github.com/muesli/cancelreader v0.2.2 // indirect
	github.com/muesli/termenv v0.16.0 // indirect
	github.com/rivo/uniseg v0.4.7 // indirect
	github.com/rogpeppe/go-internal v1.15.0 // indirect
	github.com/spf13/pflag v1.0.9 // indirect
	github.com/xo/terminfo v0.0.0-20220910002029-abceb7e1c41e // indirect
	golang.org/x/text v0.41.0 // indirect
)
