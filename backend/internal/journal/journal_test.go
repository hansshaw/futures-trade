package journal

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"cornwatch/internal/analysis"
)

func testService(t *testing.T, config analysis.Config) *Service {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "private", "journal.db"), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
func testJournal(t *testing.T, s *Service) Journal {
	t.Helper()
	j, err := s.Save(Journal{Date: "2026-10-28", Title: "玉米盘后复盘", Symbol: "c2701", Plan: "亏损预算必须在入场前确定", Discipline: 2, Tags: []string{"趋势"}}, "")
	if err != nil {
		t.Fatal(err)
	}
	return j
}
func pngURL(t *testing.T) string {
	t.Helper()
	var b bytes.Buffer
	im := image.NewRGBA(image.Rect(0, 0, 3, 2))
	im.Set(1, 1, color.RGBA{R: 255, A: 255})
	if err := png.Encode(&b, im); err != nil {
		t.Fatal(err)
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(b.Bytes())
}

func TestPersistenceRevisionArchiveAndSnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notes", "journal.db")
	s, err := Open(path, analysis.Config{})
	if err != nil {
		t.Fatal(err)
	}
	j := testJournal(t, s)
	a, err := s.AddAsset("../../盘面.png", pngURL(t))
	if err != nil {
		t.Fatal(err)
	}
	if a.Name != "盘面.png" || a.DataURL != "" {
		t.Fatalf("bad upload metadata: %+v", a)
	}
	j.AttachmentIDs = []string{a.ID}
	j, err = s.Save(j, j.ID)
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.Review(context.Background(), ReviewRequest{JournalIDs: []string{j.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if r.ImageCount != 0 || r.InputRevisions[j.ID] != 2 || len(r.Lessons) == 0 {
		t.Fatalf("bad review: %+v", r)
	}
	prior := j
	j.Review = "原记录仍可编辑"
	j, err = s.Save(j, j.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Save(prior, j.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("want conflict, got %v", err)
	}
	if _, err = s.Archive(j.ID, j.Revision, true); err != nil {
		t.Fatal(err)
	}
	if items, _ := s.Journals("", "", false); len(items) != 0 {
		t.Fatal("archived visible")
	}
	if items, _ := s.Journals("预算", "2026-10", true); len(items) != 1 {
		t.Fatal("archive search")
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path, analysis.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	loaded, err := s.Journal(j.ID)
	if err != nil || !loaded.Archived || loaded.Revision != 4 {
		t.Fatalf("lost journal: %+v %v", loaded, err)
	}
	loaded, err = s.Archive(loaded.ID, loaded.Revision, false)
	if err != nil || loaded.Archived {
		t.Fatal("restore failed", err)
	}
	asset, err := s.Asset(a.ID)
	if err != nil || asset.DataURL != pngURL(t) {
		t.Fatal("asset not durable", err)
	}
	snap, err := s.ReviewSnapshot(r.ID)
	if err != nil || snap.Journals[0].Revision != 2 || snap.Journals[0].Review != "" {
		t.Fatal("snapshot overwritten", err)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Fatal("database permissions", info.Mode())
	}
	info, _ = os.Stat(filepath.Dir(path))
	if info.Mode().Perm() != 0700 {
		t.Fatal("directory permissions", info.Mode())
	}
}

func TestConcurrentSaveOnlyOneWinner(t *testing.T) {
	s := testService(t, analysis.Config{})
	j := testJournal(t, s)
	results := make(chan error, 12)
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := s.Save(j, j.ID); results <- err }()
	}
	wg.Wait()
	close(results)
	wins := 0
	conflicts := 0
	for err := range results {
		if err == nil {
			wins++
		} else if errors.Is(err, ErrConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if wins != 1 || conflicts != 11 {
		t.Fatalf("wins %d conflicts %d", wins, conflicts)
	}
}

func TestJournalValidation(t *testing.T) {
	s := testService(t, analysis.Config{})
	for name, change := range map[string]func(*Journal){
		"date":              func(j *Journal) { j.Date = "2026-02-31" },
		"title":             func(j *Journal) { j.Title = "" },
		"long body":         func(j *Journal) { j.Plan = strings.Repeat("字", 4001) },
		"symbol":            func(j *Journal) { j.Symbol = "<script>" },
		"discipline":        func(j *Journal) { j.Discipline = 6 },
		"missing asset":     func(j *Journal) { j.AttachmentIDs = []string{strings.Repeat("a", 32)} },
		"too many assets":   func(j *Journal) { j.AttachmentIDs = make([]string, 7) },
		"supplied id":       func(j *Journal) { j.ID = strings.Repeat("a", 32) },
		"supplied revision": func(j *Journal) { j.Revision = 1 },
	} {
		t.Run(name, func(t *testing.T) {
			j := Journal{Date: "2026-10-28", Title: "测试"}
			change(&j)
			if _, err := s.Save(j, ""); err == nil {
				t.Fatal("invalid accepted")
			}
		})
	}
	if _, err := s.Save(Journal{Date: "2099-10-28", Title: "提前计划", Tags: []string{" 计划 ", "计划"}}, ""); err != nil {
		t.Fatal("future plan rejected", err)
	}
}

func TestAssetValidation(t *testing.T) {
	s := testService(t, analysis.Config{})
	valid := pngURL(t)
	_, encoded, _ := strings.Cut(valid, ",")
	raw, _ := base64.StdEncoding.DecodeString(encoded)
	oversized := append([]byte{}, raw...)
	binary.BigEndian.PutUint32(oversized[16:20], 25000)
	binary.BigEndian.PutUint32(oversized[20:24], 25000)
	binary.BigEndian.PutUint32(oversized[29:33], crc32.ChecksumIEEE(oversized[12:29]))
	for name, input := range map[string]string{
		"svg":          "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString([]byte("<svg onload='alert(1)'/>")),
		"mime spoof":   strings.Replace(valid, "image/png", "image/jpeg", 1),
		"html spoof":   "data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte("<html>not an image</html>")),
		"truncated":    "data:image/png;base64," + base64.StdEncoding.EncodeToString(raw[:len(raw)/2]),
		"too large":    "data:image/png;base64," + base64.StdEncoding.EncodeToString(make([]byte, MaxAssetBytes+1)),
		"pixel bomb":   "data:image/png;base64," + base64.StdEncoding.EncodeToString(oversized),
		"bad encoding": "data:image/png;base64,!!!",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := s.AddAsset("x.png", input); err == nil {
				t.Fatal("invalid image accepted")
			}
		})
	}
	var buf bytes.Buffer
	jpeg.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 4, 3)), nil)
	a, err := s.AddAsset("a.jpeg", "data:image/jpeg;base64,"+base64.StdEncoding.EncodeToString(buf.Bytes()))
	if err != nil || a.Width != 4 || a.MIME != "image/jpeg" {
		t.Fatal("jpeg rejected", err)
	}
	// A standard 1x1 lossless WebP fixture exercises the real WebP decoder.
	webp := "UklGRhoAAABXRUJQVlA4TA0AAAAvAAAAEAcQERGIiP4HAA=="
	a, err = s.AddAsset("a.webp", "data:image/webp;base64,"+webp)
	if err != nil || a.Width != 1 {
		t.Fatal("webp rejected", err)
	}
}

func TestAcceptedLessonsTraceableAndIdempotent(t *testing.T) {
	s := testService(t, analysis.Config{})
	j := testJournal(t, s)
	r, err := s.Review(context.Background(), ReviewRequest{JournalIDs: []string{j.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if lessons, _ := s.Lessons(); len(lessons) != 0 {
		t.Fatal("auto accepted")
	}
	l, err := s.Accept(r.ID, r.Lessons[0].ID)
	if err != nil || l.Status != "accepted" || l.ReviewID != r.ID {
		t.Fatal("accept failed", err)
	}
	again, err := s.Accept(r.ID, l.ID)
	if err != nil || !again.CreatedAt.Equal(l.CreatedAt) {
		t.Fatal("accept not idempotent", err)
	}
	next, err := s.Review(context.Background(), ReviewRequest{JournalIDs: []string{j.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if len(next.ContextLessonIDs) != 1 || next.ContextLessonIDs[0] != l.ID {
		t.Fatal("accepted lesson not used")
	}
	snap, _ := s.ReviewSnapshot(next.ID)
	if snap.AcceptedLessons[0].ReviewID != r.ID {
		t.Fatal("source provenance lost")
	}
	no := false
	isolated, err := s.Review(context.Background(), ReviewRequest{JournalIDs: []string{j.ID}, UseAcceptedLessons: &no})
	if err != nil || len(isolated.ContextLessonIDs) != 0 {
		t.Fatal("cannot exclude accepted lessons")
	}
	after, _ := s.Journal(j.ID)
	if after.Revision != j.Revision {
		t.Fatal("review mutated journal")
	}
}

func validResponse(id string) string {
	raw, _ := json.Marshal(map[string]any{"summary": "交易前风控计划已记录，后续应核对执行。", "patterns": []Pattern{{Title: "风险计划", Observation: "计划中记录了亏损预算。", JournalIDs: []string{id}}}, "actions": []string{"将实际执行与计划对照。"}, "questions": []string{}, "risk_flags": []string{"样本有限。"}, "lessons": []map[string]any{{"text": "入场前确认亏损预算。", "evidence_ids": []string{id}}}})
	return string(raw)
}
func envelope(content, finish string) map[string]any {
	return map[string]any{"choices": []map[string]any{{"message": map[string]string{"content": content}, "finish_reason": finish}}}
}

func TestLLMProtocolAndDefaultImagePrivacy(t *testing.T) {
	var journalID string
	var received map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer mock-secret" {
			t.Error("wrong request protocol")
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Error(err)
		}
		json.NewEncoder(w).Encode(envelope(validResponse(journalID), "stop"))
	}))
	defer server.Close()
	s := testService(t, analysis.Config{BaseURL: server.URL + "/v1", Model: "mock-model", APIKey: "mock-secret"})
	j := testJournal(t, s)
	journalID = j.ID
	a, _ := s.AddAsset("s.png", pngURL(t))
	j.AttachmentIDs = []string{a.ID}
	j, _ = s.Save(j, j.ID)
	r, err := s.Review(context.Background(), ReviewRequest{JournalIDs: []string{j.ID}, UseLLM: true})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(received)
	if bytes.Contains(data, []byte("data:image")) || bytes.Contains(data, []byte("mock-secret")) {
		t.Fatal("sent images by default or leaked key in payload")
	}
	if r.ImageCount != 0 || r.Model != "mock-model / journal-prompt-v1" || r.Lessons[0].Status != "candidate" {
		t.Fatal("invalid result", r)
	}
	if _, err = s.Review(context.Background(), ReviewRequest{JournalIDs: []string{j.ID}, UseLLM: true, IncludeImages: true}); err == nil {
		t.Fatal("vision bypass")
	}
	s.visionEnabled = true
	r, err = s.Review(context.Background(), ReviewRequest{JournalIDs: []string{j.ID}, UseLLM: true, IncludeImages: true})
	if err != nil {
		t.Fatal(err)
	}
	data, _ = json.Marshal(received)
	if !bytes.Contains(data, []byte(`"type":"image_url"`)) || !bytes.Contains(data, []byte("data:image/png;base64,")) || r.ImageCount != 1 {
		t.Fatal("vision content blocks missing")
	}
	snap, _ := s.ReviewSnapshot(r.ID)
	if len(snap.ImageSHA256) != 1 {
		t.Fatal("image audit hash missing")
	}
}

func TestLLMInvalidOutputDoesNotOverwrite(t *testing.T) {
	var content string
	finish := "stop"
	status := 200
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(envelope(content, finish))
	}))
	defer server.Close()
	s := testService(t, analysis.Config{BaseURL: server.URL, Model: "mock"})
	j := testJournal(t, s)
	prior, err := s.Review(context.Background(), ReviewRequest{JournalIDs: []string{j.ID}})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"hallucinated reference", "truncated", "unknown fields", "multiple JSON", "overlong", "http failure", "invalid JSON"} {
		t.Run(name, func(t *testing.T) {
			content = validResponse(j.ID)
			finish = "stop"
			status = 200
			switch name {
			case "hallucinated reference":
				content = validResponse(strings.Repeat("f", 32))
			case "truncated":
				finish = "length"
			case "unknown fields":
				content = strings.Replace(content, `"summary":`, `"tools":["run"],"summary":`, 1)
			case "multiple JSON":
				content += " {}"
			case "overlong":
				content = strings.Replace(content, "交易前风控计划已记录，后续应核对执行。", strings.Repeat("a", 12001), 1)
			case "http failure":
				status = 500
				content = "mock-secret"
			case "invalid JSON":
				content = "```json\n{}\n```"
			}
			if _, err := s.Review(context.Background(), ReviewRequest{JournalIDs: []string{j.ID}, UseLLM: true}); err == nil || strings.Contains(err.Error(), "mock-secret") {
				t.Fatal("bad output accepted or secret leaked", err)
			}
			stored, _ := s.Reviews()
			if len(stored) != 1 || stored[0].ID != prior.ID {
				t.Fatal("history overwritten")
			}
			after, _ := s.Journal(j.ID)
			if after.Revision != j.Revision {
				t.Fatal("journal overwritten")
			}
		})
	}
}

func TestReviewLimitsCancellationAndTimeout(t *testing.T) {
	s := testService(t, analysis.Config{})
	j := testJournal(t, s)
	for name, req := range map[string]ReviewRequest{
		"empty": {}, "duplicate": {JournalIDs: []string{j.ID, j.ID}}, "too many": {JournalIDs: make([]string, 21)}, "missing model": {JournalIDs: []string{j.ID}, UseLLM: true}, "images without model": {JournalIDs: []string{j.ID}, IncludeImages: true},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := s.Review(context.Background(), req); err == nil {
				t.Fatal("invalid review accepted")
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Review(ctx, ReviewRequest{JournalIDs: []string{j.ID}}); err == nil {
		t.Fatal("cancelled review saved")
	}
	for i := 0; i < cap(s.reviewSlots); i++ {
		s.reviewSlots <- struct{}{}
	}
	if _, err := s.Review(context.Background(), ReviewRequest{JournalIDs: []string{j.ID}}); !errors.Is(err, ErrBusy) {
		t.Fatal("no concurrency limit")
	}
	for len(s.reviewSlots) > 0 {
		<-s.reviewSlots
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(200 * time.Millisecond):
		}
	}))
	defer server.Close()
	llm := testService(t, analysis.Config{BaseURL: server.URL, Model: "mock"})
	j = testJournal(t, llm)
	llm.analyzer.Client.Timeout = 10 * time.Millisecond
	if _, err := llm.Review(context.Background(), ReviewRequest{JournalIDs: []string{j.ID}, UseLLM: true}); err == nil {
		t.Fatal("timeout expected")
	}
}

func TestHTTPConflictAndStrictBody(t *testing.T) {
	s := testService(t, analysis.Config{})
	j := testJournal(t, s)
	mux := http.NewServeMux()
	s.Register(mux, nil)
	j.Title = "updated"
	body, _ := json.Marshal(j)
	call := func(method, path string, body []byte) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, bytes.NewReader(body))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}
	if w := call("POST", "/api/journals/"+j.ID, body); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := call("POST", "/api/journals/"+j.ID, body); w.Code != 409 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := call("POST", "/api/journals", []byte(`{"date":"2026-10-28","title":"test","unknown":true}`)); w.Code != 400 {
		t.Fatal("unknown field accepted")
	}
	if w := call("POST", "/api/journals", []byte(`{} {}`)); w.Code != 400 {
		t.Fatal("multiple objects accepted")
	}
	if w := call("GET", "/api/journal-assets/missing", nil); w.Code != 404 {
		t.Fatal(w.Code)
	}
}

func TestReviewAggregateLimits(t *testing.T) {
	s := testService(t, analysis.Config{})
	j := testJournal(t, s)
	// Direct snapshot testing isolates aggregate limits without issuing a model call.
	ids := []string{}
	for i := 0; i < 9; i++ {
		a, err := s.AddAsset("chart.png", pngURL(t))
		if err != nil {
			t.Fatal(err)
		}
		copy := Journal{Date: j.Date, Title: j.Title, AttachmentIDs: []string{a.ID}}
		copy, err = s.Save(copy, "")
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, copy.ID)
	}
	if _, _, err := s.snapshot(ReviewRequest{JournalIDs: ids, IncludeImages: true}); err == nil {
		t.Fatal("image count limit not enforced")
	}
	if snap, images, err := s.snapshot(ReviewRequest{JournalIDs: ids, IncludeImages: false}); err != nil || len(images) != 0 || len(snap.ImageSHA256) != 0 {
		t.Fatal("nonvision snapshot touched images", err)
	}
	textIDs := []string{}
	for i := 0; i < 9; i++ {
		body := strings.Repeat("a", 12000)
		copy, err := s.Save(Journal{Date: j.Date, Title: "长文", Plan: body, Thesis: body, Execution: body, Review: body, Lessons: body, Emotion: body}, "")
		if err != nil {
			t.Fatal(err)
		}
		textIDs = append(textIDs, copy.ID)
	}
	if _, _, err := s.snapshot(ReviewRequest{JournalIDs: textIDs}); err == nil {
		t.Fatal("text limit not enforced")
	}
}

func TestConcurrentEditDuringModelPreservesInputRevision(t *testing.T) {
	started := make(chan struct{})
	resume := make(chan struct{})
	var journalID string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-resume
		json.NewEncoder(w).Encode(envelope(validResponse(journalID), "stop"))
	}))
	defer server.Close()
	s := testService(t, analysis.Config{BaseURL: server.URL, Model: "mock"})
	j := testJournal(t, s)
	journalID = j.ID
	type response struct {
		result Review
		err    error
	}
	done := make(chan response, 1)
	go func() {
		result, err := s.Review(context.Background(), ReviewRequest{JournalIDs: []string{j.ID}, UseLLM: true})
		done <- response{result, err}
	}()
	<-started
	edited := j
	edited.Plan = "新计划"
	edited, err := s.Save(edited, edited.ID)
	if err != nil {
		t.Fatal(err)
	}
	close(resume)
	out := <-done
	if out.err != nil {
		t.Fatal(out.err)
	}
	if out.result.InputRevisions[j.ID] != j.Revision {
		t.Fatal("model report claims later revision")
	}
	snap, _ := s.ReviewSnapshot(out.result.ID)
	if snap.Journals[0].Plan != j.Plan {
		t.Fatal("original input overwritten")
	}
	current, _ := s.Journal(j.ID)
	if current.Plan != "新计划" || current.Revision != edited.Revision {
		t.Fatal("AI overwrote edit")
	}
}

func TestLLMRedirectDoesNotForwardSecrets(t *testing.T) {
	called := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true; t.Error("redirect followed") }))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 307) }))
	defer server.Close()
	s := testService(t, analysis.Config{BaseURL: server.URL, Model: "mock", APIKey: "secret"})
	j := testJournal(t, s)
	if _, err := s.Review(context.Background(), ReviewRequest{JournalIDs: []string{j.ID}, UseLLM: true}); err == nil {
		t.Fatal("redirect accepted")
	}
	if called {
		t.Fatal("leaked to redirected service")
	}
}

func TestRevokeLessonKeepsHistoricalSnapshots(t *testing.T) {
	s := testService(t, analysis.Config{})
	j := testJournal(t, s)
	request := ReviewRequest{JournalIDs: []string{j.ID}}
	original, err := s.Review(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	accepted, err := s.Accept(original.ID, original.Lessons[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	withMemory, err := s.Review(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	before, err := s.ReviewSnapshot(withMemory.ID)
	if err != nil || len(before.AcceptedLessons) != 1 {
		t.Fatal("missing initial memory", err)
	}
	beforeJSON, _ := json.Marshal(before)

	revoked, err := s.Revoke(accepted.ID)
	if err != nil || revoked.Status != "candidate" || revoked.ID != accepted.ID || revoked.ReviewID != original.ID {
		t.Fatal("revoke failed", revoked, err)
	}
	if lessons, err := s.Lessons(); err != nil || len(lessons) != 0 {
		t.Fatal("revoked lesson retained", err)
	}
	repeated, err := s.Revoke(accepted.ID)
	if err != nil || !repeated.CreatedAt.Equal(revoked.CreatedAt) || repeated.Status != "candidate" {
		t.Fatal("revoke not idempotent", err)
	}
	reviews, _ := s.Reviews()
	for _, review := range reviews {
		if review.ID == original.ID && review.Lessons[0].Status != "candidate" {
			t.Fatal("original report status not restored")
		}
	}
	withoutMemory, err := s.Review(context.Background(), request)
	if err != nil || len(withoutMemory.ContextLessonIDs) != 0 {
		t.Fatal("revoked lesson reused", err)
	}
	after, _ := s.ReviewSnapshot(withMemory.ID)
	afterJSON, _ := json.Marshal(after)
	if !bytes.Equal(beforeJSON, afterJSON) {
		t.Fatal("historical input snapshot changed")
	}
	if after.AcceptedLessons[0].Status != "accepted" || after.AcceptedLessons[0].ReviewID != original.ID {
		t.Fatal("historical memory evidence lost")
	}

	acceptedAgain, err := s.Accept(original.ID, accepted.ID)
	if err != nil || acceptedAgain.Status != "accepted" || acceptedAgain.ID != accepted.ID {
		t.Fatal("cannot accept again", err)
	}
	withMemoryAgain, err := s.Review(context.Background(), request)
	if err != nil || len(withMemoryAgain.ContextLessonIDs) != 1 || withMemoryAgain.ContextLessonIDs[0] != accepted.ID {
		t.Fatal("reaccepted lesson not reused", err)
	}
	if _, err := s.Revoke(strings.Repeat("f", 32)); !errors.Is(err, ErrNotFound) {
		t.Fatal("missing lesson must return not found", err)
	}
	current, _ := s.Journal(j.ID)
	if current.Revision != j.Revision {
		t.Fatal("lesson revocation changed journal revision")
	}

	mux := http.NewServeMux()
	s.Register(mux, nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/journal-lessons/"+accepted.ID+"/revoke", strings.NewReader("{}")))
	if w.Code != http.StatusOK {
		t.Fatal("HTTP revocation failed", w.Code, w.Body.String())
	}
	var response Lesson
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || response.Status != "candidate" {
		t.Fatal("HTTP response invalid", err)
	}
}
