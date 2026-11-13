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

// legitimateVerification is the profile of every pattern that is not fraud
// and has no profile in newCardVerification. Genuine cardholders almost always
// pass. A regular player pays with the card saved on the account, and a saved
// card keeps no security code, so the gateway often skips the CVV check, as it
// does for a takeover that pays with the owner's saved card. Some issuers or
// regions skip a check, and an outdated address or a mistyped code gives a
// rare innocent mismatch.
var legitimateVerification = verificationProfile{
	cvv:     []resultShare{{"mismatch", 0.01}, {"not_checked", 0.35}},
	avs:     []resultShare{{"mismatch", 0.02}, {"partial", 0.08}, {"not_checked", 0.25}},
	threeDS: []resultShare{{"failed", 0.01}, {"attempted", 0.05}, {"not_enrolled", 0.10}},
}

// enteredCardVerification is the profile of honest purchases with a card
// typed in for the first time: on a new account, on a relative's account, or
// on an internet cafe's computer. The gateway checks the code, and a typo
// gives a mismatch more often than on a saved card.
var enteredCardVerification = verificationProfile{
	cvv:     []resultShare{{"mismatch", 0.03}, {"not_checked", 0.05}},
	avs:     legitimateVerification.avs,
	threeDS: legitimateVerification.threeDS,
}

var newCardVerification = map[Pattern]verificationProfile{
	PatternNewCasualPlayer: enteredCardVerification,
	PatternNewBigSpender:   enteredCardVerification,
	PatternFamilyCard:      enteredCardVerification,
	PatternInternetCafe:    enteredCardVerification,
}

// avsCountries are the card countries whose issuers support the address
// check. AVS is used by card issuers in only a few countries, such as the
// United States and the United Kingdom, so the gateway cannot check the
// address of any other card, honest or not.
var avsCountries = map[string]bool{"US": true, "GB": true}

// fraudVerification gives the fraud patterns their own profile. Fraudsters
// who bought full card details pass the CVV check most of the time but rarely
// know the billing address or the cardholder's 3-D Secure code. Card testers
// probe numbers without the details, so they fail far more often. A takeover
// pays with the owner's saved card, so the stored details still match and the
// gateway often skips the CVV check. Low and slow fraud pays with a card saved
// on the fraudster's own older account, so its CVV check is skipped as often as
// a regular player's.
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
		cvv:     []resultShare{{"mismatch", 0.05}, {"not_checked", 0.35}},
		avs:     []resultShare{{"mismatch", 0.08}, {"partial", 0.10}, {"not_checked", 0.25}},
		threeDS: []resultShare{{"failed", 0.08}, {"attempted", 0.08}, {"not_enrolled", 0.10}},
	},
}

// withVerification adds the gateway's verification results for the pattern.
// The results are drawn from the seed and the transaction ID, outside the
// random sequence, so the rest of the traffic is the same as without them.
func (g *generator) withVerification(pattern Pattern, req Request) Request {
	profile := verificationOf(pattern)
	req.CVVResult = ptr(drawResult(g.seededDraw(req.TransactionID, "cvv"), profile.cvv, "match"))
	req.AVSResult = ptr(drawResult(g.seededDraw(req.TransactionID, "avs"), profile.avs, "match"))
	if req.CardCountry == nil || !avsCountries[*req.CardCountry] {
		req.AVSResult = ptr("not_checked")
	}
	req.ThreeDSResult = ptr(drawResult(g.seededDraw(req.TransactionID, "three_ds"), profile.threeDS, "authenticated"))
	return req
}

func verificationOf(pattern Pattern) verificationProfile {
	if profile, fraud := fraudVerification[pattern]; fraud {
		return profile
	}
	if profile, entered := newCardVerification[pattern]; entered {
		return profile
	}
	return legitimateVerification
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
