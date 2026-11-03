package task

// Seed fills s with example data.
func Seed(s *Store) {
	s.Add("write tests", false)
	s.Add("ship it", false)
	s.Add("celebrate", true)
}
