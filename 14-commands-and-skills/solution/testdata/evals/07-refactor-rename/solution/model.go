package task

// User is a registered account.
type User struct {
	ID   int
	Name string
}

// Greeting returns a short greeting for the User.
func (u User) Greeting() string { return "hi " + u.Name }
