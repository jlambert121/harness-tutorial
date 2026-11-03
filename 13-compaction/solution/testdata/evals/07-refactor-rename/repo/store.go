package task

// Store holds Usr values by ID.
type Store struct{ byID map[int]Usr }

// NewStore returns an empty Store.
func NewStore() *Store { return &Store{byID: map[int]Usr{}} }

// Save stores u.
func (s *Store) Save(u Usr) { s.byID[u.ID] = u }

// Find returns the Usr with the given ID.
func (s *Store) Find(id int) (Usr, bool) {
	u, ok := s.byID[id]
	return u, ok
}
