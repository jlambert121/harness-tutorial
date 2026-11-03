package task

import "sort"

// Store keeps todos in memory.
type Store struct {
	next  int
	todos []Todo
}

// Add creates a todo and returns its ID.
func (s *Store) Add(title string, done bool) int {
	s.next++
	s.todos = append(s.todos, Todo{ID: s.next, Title: title, Done: done})
	return s.next
}

// List returns all todos ordered by ID.
func (s *Store) List() []Todo {
	out := append([]Todo(nil), s.todos...)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
