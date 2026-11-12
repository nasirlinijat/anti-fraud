package scoring

// PaymentVerification holds the card checks that the payment gateway runs
// during authorization. An empty field means the caller did not send it, so
// the rule that reads it does not fire.
type PaymentVerification struct {
	CVV     CVVResult
	AVS     AVSResult
	ThreeDS ThreeDSResult
}

// CVVResult is the gateway's check of the card security code.
type CVVResult string

const (
	CVVMatch      CVVResult = "match"
	CVVMismatch   CVVResult = "mismatch"
	CVVNotChecked CVVResult = "not_checked"
)

// CVVResults lists the accepted CVV results.
var CVVResults = []CVVResult{CVVMatch, CVVMismatch, CVVNotChecked}

// AVSResult is the gateway's check of the billing address against the
// issuer's records.
type AVSResult string

const (
	AVSMatch      AVSResult = "match"
	AVSPartial    AVSResult = "partial"
	AVSMismatch   AVSResult = "mismatch"
	AVSNotChecked AVSResult = "not_checked"
)

// AVSResults lists the accepted AVS results.
var AVSResults = []AVSResult{AVSMatch, AVSPartial, AVSMismatch, AVSNotChecked}

// ThreeDSResult is the outcome of the 3-D Secure cardholder authentication.
type ThreeDSResult string

const (
	ThreeDSAuthenticated ThreeDSResult = "authenticated"
	ThreeDSAttempted     ThreeDSResult = "attempted"
	ThreeDSFailed        ThreeDSResult = "failed"
	ThreeDSNotEnrolled   ThreeDSResult = "not_enrolled"
)

// ThreeDSResults lists the accepted 3-D Secure results.
var ThreeDSResults = []ThreeDSResult{ThreeDSAuthenticated, ThreeDSAttempted, ThreeDSFailed, ThreeDSNotEnrolled}

func cvvMismatch(in Input, _ RuleConfig) bool {
	return in.Verification.CVV == CVVMismatch
}

// avsMismatch fires only on a full mismatch. A partial match is common for
// genuine cardholders, for example after a move or with a differently written
// street, so it is shown as evidence but adds no points.
func avsMismatch(in Input, _ RuleConfig) bool {
	return in.Verification.AVS == AVSMismatch
}

func threeDSFailed(in Input, _ RuleConfig) bool {
	return in.Verification.ThreeDS == ThreeDSFailed
}

func cvvMismatchMessage(Input, RuleConfig) string {
	return "The card security code (CVV) did not match"
}

func avsMismatchMessage(Input, RuleConfig) string {
	return "The billing address (AVS) did not match the card issuer's records"
}

func threeDSFailedMessage(Input, RuleConfig) string {
	return "The cardholder failed 3-D Secure authentication"
}
