package task

// Store holds User values by ID.
type Store struct{ byID map[int]User }

// NewStore returns an empty Store.
func NewStore() *Store { return &Store{byID: map[int]User{}} }

// Save stores u.
func (s *Store) Save(u User) { s.byID[u.ID] = u }

// Find returns the User with the given ID.
func (s *Store) Find(id int) (User, bool) {
	u, ok := s.byID[id]
	return u, ok
}
