package journal

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	bolt "go.etcd.io/bbolt"
	"strings"
	"time"
)

// SaveGenerated creates a journal once per immutable report. A crash between the
// two databases cannot duplicate it; subsequent calls never overwrite user edits.
func (s *Service) SaveGenerated(j Journal, sourceID string) (Journal, bool, error) {
	if strings.TrimSpace(sourceID) == "" || len(sourceID) > 160 {
		return Journal{}, false, errors.New("报告来源ID无效")
	}
	if err := validateJournal(&j); err != nil {
		return Journal{}, false, err
	}
	hash := sha256.Sum256([]byte("daily-report:" + sourceID))
	id := hex.EncodeToString(hash[:16])
	existing := false
	err := s.db.Update(func(tx *bolt.Tx) error {
		links, e := tx.CreateBucketIfNotExists([]byte("generated_reports"))
		if e != nil {
			return e
		}
		if link := links.Get([]byte(sourceID)); link != nil {
			existing = true
			return get(tx, "journals", string(link), &j)
		}
		if tx.Bucket([]byte("journals")).Get([]byte(id)) != nil {
			return errors.New("报告日记标识冲突")
		}
		j.ID = id
		j.CreatedAt = time.Now().UTC()
		j.UpdatedAt = j.CreatedAt
		j.Revision = 1
		j.Archived = false
		j.AttachmentIDs = []string{}
		if err := put(tx, "journals", id, j); err != nil {
			return err
		}
		return links.Put([]byte(sourceID), []byte(id))
	})
	return j, existing, err
}
