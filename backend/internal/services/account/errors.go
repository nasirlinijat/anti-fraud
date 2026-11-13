package account

import (
	"errors"
	"fmt"
)

var (
	ErrInvalidUsername = errors.New("username must be 3 to 64 lowercase letters, digits, dots, hyphens or underscores, starting with a letter or digit")
	ErrInvalidRole     = errors.New("role must be reviewer or admin")
	ErrPasswordLength  = fmt.Errorf("password must be %d to %d bytes long", MinPasswordLength, maxPasswordLength)
	ErrDisableSelf     = errors.New("you cannot disable your own account")
)
