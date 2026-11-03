package task

import "testing"

func TestSignUp(t *testing.T) {
	if SignUp("ada", "ada@example.com") != nil || SignUp("", "a@b") == nil || SignUp("ada", "bad") != ErrInvalidEmail {
		t.Error("SignUp")
	}
}
