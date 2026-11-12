package simulator

// resultShare is the share of purchases, from 0 to 1, whose check ends in
// result.
type resultShare struct {
	result string
	share  float64
}

// verificationProfile gives, for each gateway check, the shares of the
// results other than a pass. The remaining purchases pass: the CVV and the
// address match, and 3-D Secure authenticates the cardholder.
type verificationProfile struct {
	cvv     []resultShare
	avs     []resultShare
	threeDS []resultShare
}

// legitimateVerification is the profile of every pattern that is not fraud.
// Genuine cardholders almost always pass. Some issuers or regions skip a check
// (AVS is not supported by many issuers outside a few countries), and a mistyped
// security code or an outdated address gives a rare innocent mismatch.
var legitimateVerification = verificationProfile{
	cvv:     []resultShare{{"mismatch", 0.01}, {"not_checked", 0.05}},
	avs:     []resultShare{{"mismatch", 0.02}, {"partial", 0.08}, {"not_checked", 0.25}},
	threeDS: []resultShare{{"failed", 0.01}, {"attempted", 0.05}, {"not_enrolled", 0.10}},
}

// fraudVerification gives the fraud patterns their own profile. Fraudsters
// who bought full card details pass the CVV check most of the time but rarely
// know the billing address or the cardholder's 3-D Secure code. Card testers
// probe numbers without the details, so they fail far more often. A takeover
// pays with the owner's saved card, so the stored details still match and the
// gateway often skips the CVV check.
var fraudVerification = map[Pattern]verificationProfile{
	PatternCardTesting: {
		cvv:     []resultShare{{"mismatch", 0.45}, {"not_checked", 0.15}},
		avs:     []resultShare{{"mismatch", 0.50}, {"partial", 0.10}, {"not_checked", 0.20}},
		threeDS: []resultShare{{"failed", 0.35}, {"attempted", 0.15}, {"not_enrolled", 0.25}},
	},
	PatternSlowCardTesting: {
		cvv:     []resultShare{{"mismatch", 0.30}, {"not_checked", 0.15}},
		avs:     []resultShare{{"mismatch", 0.35}, {"partial", 0.10}, {"not_checked", 0.25}},
		threeDS: []resultShare{{"failed", 0.25}, {"attempted", 0.15}, {"not_enrolled", 0.25}},
	},
	PatternStolenCard: {
		cvv:     []resultShare{{"mismatch", 0.10}, {"not_checked", 0.05}},
		avs:     []resultShare{{"mismatch", 0.30}, {"partial", 0.15}, {"not_checked", 0.25}},
		threeDS: []resultShare{{"failed", 0.40}, {"attempted", 0.10}, {"not_enrolled", 0.15}},
	},
	PatternNewAccountSpend: {
		cvv:     []resultShare{{"mismatch", 0.15}, {"not_checked", 0.05}},
		avs:     []resultShare{{"mismatch", 0.30}, {"partial", 0.15}, {"not_checked", 0.25}},
		threeDS: []resultShare{{"failed", 0.30}, {"attempted", 0.10}, {"not_enrolled", 0.15}},
	},
	PatternDeviceFarm: {
		cvv:     []resultShare{{"mismatch", 0.20}, {"not_checked", 0.10}},
		avs:     []resultShare{{"mismatch", 0.35}, {"partial", 0.10}, {"not_checked", 0.25}},
		threeDS: []resultShare{{"failed", 0.30}, {"attempted", 0.10}, {"not_enrolled", 0.20}},
	},
	PatternAccountTakeover: {
		cvv:     []resultShare{{"mismatch", 0.02}, {"not_checked", 0.40}},
		avs:     []resultShare{{"mismatch", 0.03}, {"partial", 0.08}, {"not_checked", 0.25}},
		threeDS: []resultShare{{"failed", 0.15}, {"attempted", 0.10}, {"not_enrolled", 0.10}},
	},
	PatternQuietTakeover: {
		cvv:     []resultShare{{"mismatch", 0.02}, {"not_checked", 0.40}},
		avs:     []resultShare{{"mismatch", 0.03}, {"partial", 0.08}, {"not_checked", 0.25}},
		threeDS: []resultShare{{"failed", 0.10}, {"attempted", 0.10}, {"not_enrolled", 0.10}},
	},
	PatternLowAndSlow: {
		cvv:     []resultShare{{"mismatch", 0.05}, {"not_checked", 0.05}},
		avs:     []resultShare{{"mismatch", 0.08}, {"partial", 0.10}, {"not_checked", 0.25}},
		threeDS: []resultShare{{"failed", 0.08}, {"attempted", 0.08}, {"not_enrolled", 0.10}},
	},
}

// withVerification adds the gateway's verification results for the pattern.
// The results are drawn from the seed and the transaction ID, outside the
// random sequence, so the rest of the traffic is the same as without them.
func (g *generator) withVerification(pattern Pattern, req Request) Request {
	profile, fraud := fraudVerification[pattern]
	if !fraud {
		profile = legitimateVerification
	}
	req.CVVResult = ptr(drawResult(g.seededDraw(req.TransactionID, "cvv"), profile.cvv, "match"))
	req.AVSResult = ptr(drawResult(g.seededDraw(req.TransactionID, "avs"), profile.avs, "match"))
	req.ThreeDSResult = ptr(drawResult(g.seededDraw(req.TransactionID, "three_ds"), profile.threeDS, "authenticated"))
	return req
}

// drawResult maps a draw from 0 to 1 to the result whose share covers it, or
// to pass when the draw is beyond all shares.
func drawResult(draw float64, shares []resultShare, pass string) string {
	for _, s := range shares {
		if draw < s.share {
			return s.result
		}
		draw -= s.share
	}
	return pass
}
