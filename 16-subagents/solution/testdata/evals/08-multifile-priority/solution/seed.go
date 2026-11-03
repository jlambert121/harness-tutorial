package task

// Seed fills s with example data.
func Seed(s *Store) {
	s.Add("write tests", false, 2)
	s.Add("ship it", false, 3)
	s.Add("celebrate", true, 1)
}
