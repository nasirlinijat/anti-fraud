// Package simulator generates seeded purchase traffic for demos and sends it to
// the score API. The same seed always gives the same purchases. Besides normal
// purchases it generates card testing, account takeover and new account
// patterns that make the default ruleset review or block.
package simulator
