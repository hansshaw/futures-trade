package journal

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"cornwatch/internal/analysis"
	bolt "go.etcd.io/bbolt"
)

var (
	ErrNotFound   = errors.New("记录不存在")
	ErrConflict   = errors.New("记录已在其他窗口更新，请重新打开后合并修改")
	ErrBusy       = errors.New("复盘任务繁忙，请稍后重试")
	idPattern     = regexp.MustCompile(`^[a-f0-9]{32}$`)
	symbolPattern = regexp.MustCompile(`^[a-z]{1,3}[0-9]{4}$`)
)

type Service struct {
	db            *bolt.DB
	analyzer      *analysis.Analyzer
	visionEnabled bool
	reviewSlots   chan struct{}
	assetSlots    chan struct{}
}

func Open(path string, config analysis.Config) (*Service, error) {
	a, err := analysis.New(config)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	if info, e := os.Lstat(path); e == nil && !info.Mode().IsRegular() {
		return nil, errors.New("日记数据库必须是普通文件")
	} else if e != nil && !errors.Is(e, os.ErrNotExist) {
		return nil, e
	}
	db, err := bolt.Open(path, 0600, &bolt.Options{Timeout: time.Second})
	if err != nil {
		return nil, err
	}
	if err = os.Chmod(path, 0600); err != nil {
		db.Close()
		return nil, err
	}
	err = db.Update(func(tx *bolt.Tx) error {
		for _, name := range []string{"journals", "assets", "reviews", "snapshots", "lessons"} {
			if _, e := tx.CreateBucketIfNotExists([]byte(name)); e != nil {
				return e
			}
		}
		return nil
	})
	if err != nil {
		db.Close()
		return nil, err
	}
	return &Service{db: db, analyzer: a, visionEnabled: strings.EqualFold(os.Getenv("LLM_VISION_ENABLED"), "true"), reviewSlots: make(chan struct{}, 2), assetSlots: make(chan struct{}, 2)}, nil
}

func (s *Service) Close() error        { return s.db.Close() }
func (s *Service) VisionEnabled() bool { return s.visionEnabled }

func newID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
func get(tx *bolt.Tx, bucket, id string, value any) error {
	b := tx.Bucket([]byte(bucket)).Get([]byte(id))
	if b == nil {
		return ErrNotFound
	}
	return json.Unmarshal(b, value)
}
func put(tx *bolt.Tx, bucket, id string, value any) error {
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return tx.Bucket([]byte(bucket)).Put([]byte(id), b)
}

func validateJournal(j *Journal) error {
	j.Title = strings.TrimSpace(j.Title)
	j.Symbol = strings.ToLower(strings.TrimSpace(j.Symbol))
	date, err := time.Parse("2006-01-02", j.Date)
	if err != nil || date.Format("2006-01-02") != j.Date {
		return errors.New("日记日期应为 YYYY-MM-DD，可填写提前计划日期")
	}
	if j.Title == "" || len(j.Title) > 400 {
		return errors.New("日记标题必填，最多400字节")
	}
	if j.Symbol != "" && !symbolPattern.MatchString(j.Symbol) {
		return errors.New("合约应为实际合约，如 c2701；也可留空")
	}
	if j.Discipline < 0 || j.Discipline > 5 {
		return errors.New("纪律评分应为0至5的整数")
	}
	if j.PNL != nil && (math.IsNaN(*j.PNL) || math.IsInf(*j.PNL, 0) || math.Abs(*j.PNL) > 1e15) {
		return errors.New("盈亏必须为有效有限数值")
	}
	for _, text := range []string{j.Title, j.Plan, j.Thesis, j.Execution, j.Review, j.Lessons, j.Emotion} {
		if !utf8.ValidString(text) || len(text) > 12000 {
			return errors.New("每个正文字段最多12000字节，需为有效文字")
		}
	}
	if len(j.Tags) > 20 {
		return errors.New("每篇最多20个标签")
	}
	tags := make([]string, 0, len(j.Tags))
	seen := map[string]bool{}
	for _, tag := range j.Tags {
		tag = strings.TrimSpace(tag)
		if len(tag) > 120 || !utf8.ValidString(tag) {
			return errors.New("标签最多120字节")
		}
		if tag != "" && !seen[tag] {
			tags = append(tags, tag)
			seen[tag] = true
		}
	}
	j.Tags = tags
	if len(j.AttachmentIDs) > 6 {
		return errors.New("每篇日记最多6张截图")
	}
	seen = map[string]bool{}
	for _, id := range j.AttachmentIDs {
		if !idPattern.MatchString(id) || seen[id] {
			return errors.New("截图ID无效或重复")
		}
		seen[id] = true
	}
	if j.AttachmentIDs == nil {
		j.AttachmentIDs = []string{}
	}
	return nil
}

func (s *Service) Save(j Journal, id string) (Journal, error) {
	if err := validateJournal(&j); err != nil {
		return Journal{}, err
	}
	if id == "" && (j.ID != "" || j.Revision != 0 || j.Archived) {
		return Journal{}, errors.New("新建日记不可指定ID、版本或归档状态")
	}
	if id != "" && (!idPattern.MatchString(id) || (j.ID != "" && j.ID != id)) {
		return Journal{}, errors.New("日记ID不一致")
	}
	if id == "" {
		var err error
		j.ID, err = newID()
		if err != nil {
			return Journal{}, err
		}
	}
	err := s.db.Update(func(tx *bolt.Tx) error {
		if id != "" {
			var old Journal
			if err := get(tx, "journals", id, &old); err != nil {
				return err
			}
			if j.Revision != old.Revision {
				return ErrConflict
			}
			j.ID = id
			j.CreatedAt = old.CreatedAt
			j.Archived = old.Archived
		} else {
			j.CreatedAt = time.Now().UTC()
		}
		for _, assetID := range j.AttachmentIDs {
			if tx.Bucket([]byte("assets")).Get([]byte(assetID)) == nil {
				return fmt.Errorf("截图 %s 不存在", assetID)
			}
		}
		j.Revision++
		j.UpdatedAt = time.Now().UTC()
		return put(tx, "journals", j.ID, j)
	})
	return j, err
}

func (s *Service) Journal(id string) (Journal, error) {
	var j Journal
	err := s.db.View(func(tx *bolt.Tx) error { return get(tx, "journals", id, &j) })
	return j, err
}

func (s *Service) Journals(q, month string, archived bool) ([]Journal, error) {
	if len(q) > 500 {
		return nil, errors.New("搜索词过长")
	}
	if month != "" {
		if d, err := time.Parse("2006-01", month); err != nil || d.Format("2006-01") != month {
			return nil, errors.New("月份应为 YYYY-MM")
		}
	}
	out := []Journal{}
	q = strings.ToLower(q)
	err := s.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte("journals")).ForEach(func(_, v []byte) error {
			var j Journal
			if err := json.Unmarshal(v, &j); err != nil {
				return err
			}
			if j.Archived != archived || (month != "" && !strings.HasPrefix(j.Date, month)) {
				return nil
			}
			text := strings.ToLower(strings.Join([]string{j.Title, j.Symbol, strings.Join(j.Tags, " "), j.Plan, j.Thesis, j.Execution, j.Review, j.Lessons, j.Emotion}, " "))
			if q == "" || strings.Contains(text, q) {
				out = append(out, j)
			}
			return nil
		})
	})
	sort.Slice(out, func(i, j int) bool {
		if out[i].Date == out[j].Date {
			return out[i].UpdatedAt.After(out[j].UpdatedAt)
		}
		return out[i].Date > out[j].Date
	})
	return out, err
}

func (s *Service) Archive(id string, revision uint64, archived bool) (Journal, error) {
	var j Journal
	err := s.db.Update(func(tx *bolt.Tx) error {
		if err := get(tx, "journals", id, &j); err != nil {
			return err
		}
		if j.Revision != revision {
			return ErrConflict
		}
		j.Archived = archived
		j.Revision++
		j.UpdatedAt = time.Now().UTC()
		return put(tx, "journals", id, j)
	})
	return j, err
}

func listLessons(tx *bolt.Tx) ([]Lesson, error) {
	out := []Lesson{}
	err := tx.Bucket([]byte("lessons")).ForEach(func(_, v []byte) error {
		var l Lesson
		if err := json.Unmarshal(v, &l); err != nil {
			return err
		}
		out = append(out, l)
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if len(out) > 30 {
		out = out[:30]
	}
	return out, err
}
func (s *Service) Lessons() ([]Lesson, error) {
	var out []Lesson
	err := s.db.View(func(tx *bolt.Tx) error { var err error; out, err = listLessons(tx); return err })
	return out, err
}
func (s *Service) Reviews() ([]Review, error) {
	out := []Review{}
	err := s.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte("reviews")).ForEach(func(_, v []byte) error {
			var r Review
			if err := json.Unmarshal(v, &r); err != nil {
				return err
			}
			out = append(out, r)
			return nil
		})
	})
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if len(out) > 20 {
		out = out[:20]
	}
	return out, err
}

func (s *Service) Accept(reviewID, lessonID string) (Lesson, error) {
	var result Lesson
	err := s.db.Update(func(tx *bolt.Tx) error {
		var review Review
		if err := get(tx, "reviews", reviewID, &review); err != nil {
			return err
		}
		for i, l := range review.Lessons {
			if l.ID != lessonID {
				continue
			}
			if l.Status == "accepted" {
				return get(tx, "lessons", lessonID, &result)
			}
			l.Status = "accepted"
			l.ReviewID = reviewID
			l.CreatedAt = time.Now().UTC()
			result = l
			review.Lessons[i] = l
			if err := put(tx, "lessons", lessonID, l); err != nil {
				return err
			}
			return put(tx, "reviews", reviewID, review)
		}
		return ErrNotFound
	})
	return result, err
}

// Revoke removes a lesson from future context, while preserving past snapshots.
// Looking up the immutable candidate in its original review also makes repeated
// revocations idempotent after the accepted-lesson entry has been removed.
func (s *Service) Revoke(lessonID string) (Lesson, error) {
	var result Lesson
	if !idPattern.MatchString(lessonID) {
		return result, ErrNotFound
	}
	err := s.db.Update(func(tx *bolt.Tx) error {
		cursor := tx.Bucket([]byte("reviews")).Cursor()
		for _, data := cursor.First(); data != nil; _, data = cursor.Next() {
			var review Review
			if err := json.Unmarshal(data, &review); err != nil {
				return err
			}
			for i, lesson := range review.Lessons {
				if lesson.ID != lessonID {
					continue
				}
				lesson.Status = "candidate"
				lesson.ReviewID = review.ID
				lesson.CreatedAt = review.CreatedAt
				result = lesson
				review.Lessons[i] = lesson
				if err := tx.Bucket([]byte("lessons")).Delete([]byte(lessonID)); err != nil {
					return err
				}
				return put(tx, "reviews", review.ID, review)
			}
		}
		return ErrNotFound
	})
	return result, err
}

// ReviewSnapshot is read-only and enables exported audit tooling and tests to
// prove which revisions and accepted lessons informed an earlier analysis.
func (s *Service) ReviewSnapshot(id string) (Snapshot, error) {
	var snap Snapshot
	err := s.db.View(func(tx *bolt.Tx) error { return get(tx, "snapshots", id, &snap) })
	return snap, err
}
