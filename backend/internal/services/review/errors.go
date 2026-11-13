package review

import "errors"

var (
	ErrInvalidDecision        = errors.New("decision must be approve or reject")
	ErrBlockDeviceNeedsReject = errors.New("block_device is only allowed with a reject decision")
	ErrPurchaseNotFound       = errors.New("purchase not found")
	ErrNotUnderReview         = errors.New("purchase was not scored for review")
	ErrAlreadyDecided         = errors.New("purchase has already been decided")
	ErrNotClaimed             = errors.New("purchase must be claimed by you before you decide it")
	ErrClaimedByOther         = errors.New("purchase is claimed by another account")
	ErrNoOpenClaim            = errors.New("purchase has no open claim")
)
