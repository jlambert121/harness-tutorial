SignUp and UpdateProfile in users.go duplicate the email validation. Extract it into a single unexported function validateEmail(email string) error and use it from both. Behavior must not change.
