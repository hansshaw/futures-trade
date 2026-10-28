// Package store provides durable, transactional local storage. Demo and imported
// bars have separate keyspaces so a sample can never overwrite a real contract.
package store

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"cornwatch/internal/domain"
	bolt "go.etcd.io/bbolt"
)

type Store struct{ db *bolt.DB }
type ImportInfo struct {
	Source     string    `json:"source"`
	ImportedAt time.Time `json:"imported_at"`
	Count      int       `json:"count"`
}

func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	db, err := bolt.Open(path, 0600, &bolt.Options{Timeout: time.Second})
	if err != nil {
		return nil, err
	}
	err = db.Update(func(tx *bolt.Tx) error {
		for _, name := range []string{"bars", "bar_days", "events", "observations", "runs", "meta", "imports", "analysis_history"} {
			if _, e := tx.CreateBucketIfNotExists([]byte(name)); e != nil {
				return e
			}
		}
		// Rebuild/migrate the daily uniqueness index inside the opening transaction.
		return tx.Bucket([]byte("bars")).ForEach(func(k, v []byte) error {
			var b domain.Bar
			if e := json.Unmarshal(v, &b); e != nil {
				return e
			}
			day := []byte(fmt.Sprintf("%t|%s|%s", b.IsDemo, b.Symbol, b.TradeDate))
			idx := tx.Bucket([]byte("bar_days"))
			if old := idx.Get(day); old != nil && !bytes.Equal(old, k) {
				return fmt.Errorf("conflicting daily bars for %s", day)
			}
			return idx.Put(day, k)
		})
	})
	if err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}
func (s *Store) Close() error { return s.db.Close() }
func ID(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))[:24]
}
func barKey(b domain.Bar) []byte {
	return []byte(fmt.Sprintf("%t|%s|%s", b.IsDemo, b.Symbol, b.Timestamp.UTC().Format(time.RFC3339Nano)))
}

// ImportBars is atomic. Duplicate keys are immutable; conflicting corrections
// must be versioned upstream, never silently replace a backtest's inputs.
func (s *Store) ImportBars(bars []domain.Bar, source string) (int, int, error) {
	added, duplicates := 0, 0
	err := s.db.Update(func(tx *bolt.Tx) error {
		bkt := tx.Bucket([]byte("bars"))
		for _, b := range bars {
			key := barKey(b)
			day := []byte(fmt.Sprintf("%t|%s|%s", b.IsDemo, b.Symbol, b.TradeDate))
			idx := tx.Bucket([]byte("bar_days"))
			if old := idx.Get(day); old != nil && !bytes.Equal(old, key) {
				return fmt.Errorf("%s %s 已存在不同结束时间的日K", b.Symbol, b.TradeDate)
			}
			data, e := json.Marshal(b)
			if e != nil {
				return e
			}
			if old := bkt.Get(key); old != nil {
				if !bytes.Equal(old, data) {
					return fmt.Errorf("conflicting immutable bar %s %s", b.Symbol, b.TradeDate)
				}
				duplicates++
				continue
			}
			if e = bkt.Put(key, data); e != nil {
				return e
			}
			if e = idx.Put(day, key); e != nil {
				return e
			}
			added++
		}
		if added > 0 {
			info, _ := json.Marshal(ImportInfo{source, time.Now().UTC(), added})
			return tx.Bucket([]byte("imports")).Put([]byte(ID(source, time.Now().String())), info)
		}
		return nil
	})
	if err != nil {
		return 0, 0, err
	}
	return added, duplicates, nil
}
func (s *Store) Bars(symbol string, demo bool) ([]domain.Bar, error) {
	result := []domain.Bar{}
	err := s.db.View(func(tx *bolt.Tx) error {
		c := tx.Bucket([]byte("bars")).Cursor()
		prefix := []byte(fmt.Sprintf("%t|", demo))
		if symbol != "" {
			prefix = append(prefix, []byte(symbol+"|")...)
		}
		for k, v := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, v = c.Next() {
			var b domain.Bar
			if e := json.Unmarshal(v, &b); e != nil {
				return e
			}
			result = append(result, b)
		}
		return nil
	})
	sort.Slice(result, func(i, j int) bool {
		if result[i].Timestamp.Equal(result[j].Timestamp) {
			return result[i].Symbol < result[j].Symbol
		}
		return result[i].Timestamp.Before(result[j].Timestamp)
	})
	return result, err
}
func (s *Store) AddEvent(event domain.Event) (domain.Event, bool, error) {
	created := false
	err := s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte("events"))
		if old := b.Get([]byte(event.ID)); old != nil {
			return json.Unmarshal(old, &event)
		}
		data, e := json.Marshal(event)
		if e != nil {
			return e
		}
		created = true
		return b.Put([]byte(event.ID), data)
	})
	return event, created, err
}
func (s *Store) Event(id string) (domain.Event, error) {
	var e domain.Event
	err := s.db.View(func(tx *bolt.Tx) error {
		v := tx.Bucket([]byte("events")).Get([]byte(id))
		if v == nil {
			return errors.New("event not found")
		}
		return json.Unmarshal(v, &e)
	})
	return e, err
}
func (s *Store) AnalyzeEvent(id string, a domain.Analysis) (domain.Event, error) {
	var e domain.Event
	err := s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte("events"))
		old := b.Get([]byte(id))
		if old == nil {
			return errors.New("event not found")
		}
		if err := json.Unmarshal(old, &e); err != nil {
			return err
		}
		e.Analysis = &a
		data, err := json.Marshal(e)
		if err != nil {
			return err
		}
		if err = b.Put([]byte(id), data); err != nil {
			return err
		}
		return tx.Bucket([]byte("analysis_history")).Put([]byte(id+"|"+a.AnalyzedAt.UTC().Format(time.RFC3339Nano)), data)
	})
	return e, err
}
func (s *Store) Events() ([]domain.Event, error) {
	out := []domain.Event{}
	err := s.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte("events")).ForEach(func(k, v []byte) error {
			var e domain.Event
			if err := json.Unmarshal(v, &e); err != nil {
				return err
			}
			out = append(out, e)
			return nil
		})
	})
	sort.Slice(out, func(i, j int) bool { return out[i].AvailableAt.After(out[j].AvailableAt) })
	return out, err
}
func (s *Store) AddObservations(items []domain.Observation) (int, error) {
	count := 0
	err := s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte("observations"))
		for _, o := range items {
			if b.Get([]byte(o.ID)) != nil {
				continue
			}
			data, err := json.Marshal(o)
			if err != nil {
				return err
			}
			if err = b.Put([]byte(o.ID), data); err != nil {
				return err
			}
			count++
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return count, nil
}
func (s *Store) Observation(id string) (domain.Observation, error) {
	var o domain.Observation
	err := s.db.View(func(tx *bolt.Tx) error {
		data := tx.Bucket([]byte("observations")).Get([]byte(id))
		if data == nil {
			return os.ErrNotExist
		}
		return json.Unmarshal(data, &o)
	})
	return o, err
}
func (s *Store) Observations() ([]domain.Observation, error) {
	out := []domain.Observation{}
	err := s.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte("observations")).ForEach(func(k, v []byte) error {
			var o domain.Observation
			if err := json.Unmarshal(v, &o); err != nil {
				return err
			}
			out = append(out, o)
			return nil
		})
	})
	sort.Slice(out, func(i, j int) bool { return out[i].AvailableAt.After(out[j].AvailableAt) })
	return out, err
}
func (s *Store) PutRun(id string, value any) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		data, err := json.Marshal(value)
		if err != nil {
			return err
		}
		return tx.Bucket([]byte("runs")).Put([]byte(id), data)
	})
}
func (s *Store) Runs() ([]json.RawMessage, error) {
	out := []json.RawMessage{}
	err := s.db.View(func(tx *bolt.Tx) error {
		c := tx.Bucket([]byte("runs")).Cursor()
		for k, v := c.Last(); k != nil && len(out) < 20; k, v = c.Prev() {
			out = append(out, append(json.RawMessage{}, v...))
		}
		return nil
	})
	return out, err
}
func (s *Store) SetMeta(key string, value any) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		b, e := json.Marshal(value)
		if e != nil {
			return e
		}
		return tx.Bucket([]byte("meta")).Put([]byte(key), b)
	})
}
func (s *Store) GetMeta(key string, value any) error {
	return s.db.View(func(tx *bolt.Tx) error {
		data := tx.Bucket([]byte("meta")).Get([]byte(key))
		if data == nil {
			return os.ErrNotExist
		}
		return json.Unmarshal(data, value)
	})
}
