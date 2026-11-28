package app

import "errors"

// PLACEHOLDER — delete when merging with the detection branch, whose
// certs_http.go defines these for real: the SSL certificates app's UI
// (feature/ssl-certs-ui) is written against them before that branch is
// in. Their values here only let the UI's tests build.
const (
	// certHTTPSplit: port 80 is used, and the challenge path alone is
	// split off it for about a minute (iptables or nft); the rest goes to
	// the service as before, which sees visitors as 127.0.0.1 meanwhile.
	certHTTPSplit certHTTPMode = 90
	// certConfirmSplit: that split, waiting for the user's go-ahead;
	// target is "iptables" or "nft", found.servers the programs on port 80.
	certConfirmSplit certConfirmKind = 90
)

// errCertHTTPICP: the server's cloud (in mainland China) blocks the
// domain, which has no ICP filing: only DNS proves it.
var errCertHTTPICP = errors.New("the server's cloud blocks domains without an ICP filing")
