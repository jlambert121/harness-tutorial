# Conventions
- Errors returned from this package start with the prefix "config: ".
- Wrap sentinel errors with %w so callers can use errors.Is.
- Empty input (after trimming whitespace) must return the sentinel ErrEmpty, which you should declare if it does not exist.
