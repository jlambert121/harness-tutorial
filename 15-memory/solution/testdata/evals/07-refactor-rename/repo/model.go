package task

// Usr is a registered account.
type Usr struct {
	ID   int
	Name string
}

// Greeting returns a short greeting for the Usr.
func (u Usr) Greeting() string { return "hi " + u.Name }
