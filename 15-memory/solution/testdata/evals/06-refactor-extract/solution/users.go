package task

import (
	"errors"
	"strings"
)

var ErrInvalidEmail = errors.New("invalid email")

func validateEmail(email string) error {
	at := strings.Index(email, "@")
	if at < 1 || at == len(email)-1 || strings.Count(email, "@") != 1 || strings.ContainsAny(email, " \t") {
		return ErrInvalidEmail
	}
	return nil
}

// SignUp validates a new account.
func SignUp(name, email string) error {
	if name == "" {
		return errors.New("name required")
	}
	return validateEmail(email)
}

// UpdateProfile validates a profile change.
func UpdateProfile(email string) error {
	return validateEmail(email)
}
