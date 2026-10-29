package store

import (
	"encoding/json"
	bolt "go.etcd.io/bbolt"
	"strings"
)

// SetMetaBatch commits report, job, and schedule reservations together.
func (s *Store) SetMetaBatch(values map[string]any) error {
	encoded := make(map[string][]byte, len(values))
	for key, value := range values {
		b, err := json.Marshal(value)
		if err != nil {
			return err
		}
		encoded[key] = b
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte("meta"))
		for key, value := range encoded {
			if err := b.Put([]byte(key), value); err != nil {
				return err
			}
		}
		return nil
	})
}

// MetaPrefix returns newest lexicographic keys, copying transaction-owned bytes.
func (s *Store) MetaPrefix(prefix string, limit int) ([]json.RawMessage, error) {
	out := []json.RawMessage{}
	if limit < 1 || limit > 1000 {
		limit = 100
	}
	err := s.db.View(func(tx *bolt.Tx) error {
		c := tx.Bucket([]byte("meta")).Cursor()
		k, v := c.Seek([]byte(prefix + "\xff"))
		if k == nil {
			k, v = c.Last()
		} else {
			k, v = c.Prev()
		}
		for ; k != nil && strings.HasPrefix(string(k), prefix) && len(out) < limit; k, v = c.Prev() {
			out = append(out, append(json.RawMessage{}, v...))
		}
		return nil
	})
	return out, err
}
