package auth

// Name identifies the auth subsystem.
const Name = "auth"

// Ready reports whether the auth subsystem is configured.
func Ready() bool { return true }
