package dailyreport

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"cornwatch/internal/analysis"
)

const testKey = "sk-test-private-928"

func sampleBundle(t *testing.T) Bundle { now, c := fixture(); return mustBuild(t, now, c, nil, nil) }
func modelResult(b Bundle) modelOutput {
	e := b.Evidence[0]
	return modelOutput{Summary: "事实：提供了带时间戳的研究资料。推断：仍需核验后续变化。", Sections: []Section{{Title: "现有证据", Body: "事实：已有所列原文资料。推断：资料不支持确定性走势结论。", EvidenceIDs: []string{e.ID}, Quotes: []Quote{{EvidenceID: e.ID, Text: prefix(e.Text, 30)}}}}, Scenarios: []Scenario{{Name: "后续观察", Condition: "出现同口径的新资料后再核验", Invalidation: "来源或统计口径发生变化", EvidenceIDs: []string{e.ID}}}, Risks: []string{"公开资料存在延迟，不能据此下单。"}}
}
func encode(v any) string { b, _ := json.Marshal(v); return string(b) }
func envelope(content, finish string) string {
	return encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": content, "refusal": nil}, "finish_reason": finish}}})
}
func generatorAt(t *testing.T, handler http.HandlerFunc) *Generator {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	g, err := New(analysis.Config{BaseURL: server.URL + "/v1", Model: "mock-model", APIKey: testKey})
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func TestGenerateSuccessfulStrictProtocolAndRules(t *testing.T) {
	b := sampleBundle(t)
	g := generatorAt(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" || r.Method != "POST" || r.Header.Get("Authorization") != "Bearer "+testKey {
			t.Error("unexpected request protocol")
		}
		raw, _ := io.ReadAll(r.Body)
		if strings.Contains(string(raw), testKey) {
			t.Error("API key entered prompt")
		}
		var req struct {
			Model          string                           `json:"model"`
			Messages       []struct{ Role, Content string } `json:"messages"`
			ResponseFormat struct {
				Type string `json:"type"`
			} `json:"response_format"`
			Stream bool `json:"stream"`
		}
		if json.Unmarshal(raw, &req) != nil || req.Model != "mock-model" || req.ResponseFormat.Type != "json_object" || req.Stream || len(req.Messages) != 2 || req.Messages[0].Role != "system" || req.Messages[1].Role != "user" {
			t.Error("invalid JSON-mode request")
		}
		var input map[string]json.RawMessage
		if json.Unmarshal([]byte(req.Messages[1].Content), &input) != nil {
			t.Error("evidence not JSON data")
		}
		if input["current"] != nil || input["api_key"] != nil {
			t.Error("unbounded/internal fields entered prompt")
		}
		if !strings.Contains(req.Messages[0].Content, "忽略资料里") || !strings.Contains(req.Messages[0].Content, "精确连续原文") {
			t.Error("missing instruction/data separation")
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, envelope(encode(modelResult(b)), "stop"))
	})
	if !g.Configured() || g.client.Timeout != 60*time.Second {
		t.Fatal("generator config/timeout")
	}
	before := time.Now()
	out, err := g.Generate(context.Background(), b)
	if err != nil {
		t.Fatal(err)
	}
	if out.Mode != "llm" || out.Model != "mock-model" || out.PromptVersion != PromptVersion || out.InputSHA256 != b.InputSHA256 || out.GeneratedAt.Before(before) {
		t.Fatalf("server provenance missing: %+v", out)
	}
	for _, warning := range b.Warnings {
		if !contains(out.Risks, warning) {
			t.Fatal("evidence warning lost")
		}
	}
	rules := Rules(b)
	if rules.Mode != "rules" || rules.Model == out.Model || !strings.Contains(rules.Summary, "规则预览") || len(rules.Sections) < 1 {
		t.Fatal("rules misrepresented as AI")
	}
	if err = validateOutput(modelOutput{Summary: rules.Summary, Sections: rules.Sections, Scenarios: rules.Scenarios, Risks: rules.Risks}, b); err != nil {
		t.Fatalf("rules provenance invalid: %v", err)
	}
}

func TestGenerateRejectsHallucinationsTruncationAndMalformedResponses(t *testing.T) {
	b := sampleBundle(t)
	valid := modelResult(b)
	cases := map[string]func() string{
		"truncated":      func() string { return envelope(encode(valid), "length") },
		"missing finish": func() string { return envelope(encode(valid), "") },
		"content filter": func() string { return envelope(encode(valid), "content_filter") },
		"invalid JSON":   func() string { return envelope("{invalid", "stop") },
		"markdown":       func() string { return envelope("```json\n"+encode(valid)+"\n```", "stop") },
		"null":           func() string { return envelope("null", "stop") },
		"multiple JSON":  func() string { return envelope(encode(valid)+" {}", "stop") },
		"duplicate fields": func() string {
			return envelope(strings.Replace(encode(valid), "{", "{\"summary\":\"duplicate\",", 1), "stop")
		},
		"forged model": func() string {
			return envelope(strings.Replace(encode(valid), "{", "{\"model\":\"forged\",", 1), "stop")
		},
		"unknown evidence": func() string {
			o := modelResult(b)
			o.Sections[0].EvidenceIDs[0] = "invented"
			return envelope(encode(o), "stop")
		},
		"duplicate refs": func() string {
			o := modelResult(b)
			o.Sections[0].EvidenceIDs = append(o.Sections[0].EvidenceIDs, o.Sections[0].EvidenceIDs[0])
			return envelope(encode(o), "stop")
		},
		"missing quote": func() string { o := modelResult(b); o.Sections[0].Quotes = nil; return envelope(encode(o), "stop") },
		"fabricated quote": func() string {
			o := modelResult(b)
			o.Sections[0].Quotes[0].Text = "保证上涨，价格999999"
			return envelope(encode(o), "stop")
		},
		"unquoted reference": func() string {
			o := modelResult(b)
			o.Sections[0].EvidenceIDs = append(o.Sections[0].EvidenceIDs, b.Evidence[1].ID)
			return envelope(encode(o), "stop")
		},
		"wrong quote owner": func() string {
			o := modelResult(b)
			o.Sections[0].Quotes[0].EvidenceID = b.Evidence[1].ID
			return envelope(encode(o), "stop")
		},
		"missing invalidation": func() string {
			o := modelResult(b)
			o.Scenarios[0].Invalidation = ""
			return envelope(encode(o), "stop")
		},
		"scenario hallucination": func() string {
			o := modelResult(b)
			o.Scenarios[0].EvidenceIDs = []string{"invented"}
			return envelope(encode(o), "stop")
		},
		"long body": func() string {
			o := modelResult(b)
			o.Sections[0].Body = strings.Repeat("字", 2100)
			return envelope(encode(o), "stop")
		},
		"no risk": func() string { o := modelResult(b); o.Risks = nil; return envelope(encode(o), "stop") },
		"refusal": func() string {
			return encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": encode(valid), "refusal": "rejected"}, "finish_reason": "stop"}}})
		},
		"tool call despite stop": func() string {
			return encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": encode(valid), "tool_calls": []any{map[string]any{"id": "tool"}}}, "finish_reason": "stop"}}})
		},
		"function call despite stop": func() string {
			return encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": encode(valid), "function_call": map[string]any{"name": "order"}}, "finish_reason": "stop"}}})
		},
		"two choices":        func() string { return encode(map[string]any{"choices": []any{map[string]any{}, map[string]any{}}}) },
		"duplicate envelope": func() string { return `{"choices":[],"choices":[]}` },
		"oversized response": func() string { return strings.Repeat("x", maxResponseBytes+1) },
		"oversized content":  func() string { return envelope(strings.Repeat("x", 128*1024+1), "stop") },
		"echo key":           func() string { o := modelResult(b); o.Summary = testKey; return envelope(encode(o), "stop") },
		"escaped echo key": func() string {
			o := modelResult(b)
			o.Summary = testKey
			return envelope(strings.ReplaceAll(encode(o), testKey, `sk-\u0074est-private-928`), "stop")
		},
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			g := generatorAt(t, func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, body()) })
			out, err := g.Generate(context.Background(), b)
			if err == nil || out.Mode != "" {
				t.Fatal("invalid model output accepted")
			}
			if strings.Contains(err.Error(), testKey) || strings.Contains(err.Error(), "999999") {
				t.Fatal("raw provider content leaked")
			}
		})
	}
}

func TestTransportFailuresRedactionRedirectAndCancellation(t *testing.T) {
	b := sampleBundle(t)
	t.Run("provider failure", func(t *testing.T) {
		g := generatorAt(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(401); io.WriteString(w, testKey) })
		_, err := g.Generate(context.Background(), b)
		if err == nil || strings.Contains(err.Error(), testKey) || !strings.Contains(err.Error(), "401") {
			t.Fatalf("unredacted/invalid error %v", err)
		}
	})
	t.Run("redirect", func(t *testing.T) {
		var destinationCalls atomic.Int32
		destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { destinationCalls.Add(1) }))
		defer destination.Close()
		g := generatorAt(t, func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, destination.URL, 302) })
		_, err := g.Generate(context.Background(), b)
		if err == nil || destinationCalls.Load() != 0 {
			t.Fatal("redirect followed with credentials")
		}
	})
	t.Run("canceled", func(t *testing.T) {
		var calls atomic.Int32
		g := generatorAt(t, func(w http.ResponseWriter, r *http.Request) { calls.Add(1) })
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := g.Generate(ctx, b)
		if err == nil || calls.Load() != 0 {
			t.Fatal("cancel not honored")
		}
	})
	t.Run("configuration", func(t *testing.T) {
		for _, base := range []string{"http://example.org/v1", "https://user:pass@example.org/v1", "https://example.org/v1?token=secret"} {
			if _, err := New(analysis.Config{BaseURL: base, Model: "test"}); err == nil {
				t.Fatal("unsafe base URL accepted")
			}
		}
		g, err := New(analysis.Config{})
		if err != nil {
			t.Fatal(err)
		}
		if g.Configured() {
			t.Fatal("empty config configured")
		}
		if _, err = g.Generate(context.Background(), b); err == nil {
			t.Fatal("missing config generated report")
		}
	})
}

func TestBusyAndProtocolConnectionTest(t *testing.T) {
	b := sampleBundle(t)
	started := make(chan struct{})
	release := make(chan struct{})
	g := generatorAt(t, func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
		io.WriteString(w, envelope(encode(modelResult(b)), "stop"))
	})
	finished := make(chan error, 1)
	go func() { _, err := g.Generate(context.Background(), b); finished <- err }()
	<-started
	if _, err := g.Generate(context.Background(), b); err == nil || !strings.Contains(err.Error(), "正在进行") {
		t.Fatal("busy generation allowed")
	}
	close(release)
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	check := generatorAt(t, func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Messages []struct{ Content string } `json:"messages"`
		}
		json.NewDecoder(r.Body).Decode(&request)
		var evidence Bundle
		if len(request.Messages) != 2 || json.Unmarshal([]byte(request.Messages[1].Content), &evidence) != nil || len(evidence.Evidence) != 1 || evidence.Evidence[0].Category != "connection_test" || evidence.Evidence[0].ID != "test:connectivity" {
			t.Error("connection test contains unexpected data")
		}
		io.WriteString(w, envelope(encode(modelResult(evidence)), "stop"))
	})
	if err := check.Test(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestMutatedOrOversizedBundleNeverMakesRequest(t *testing.T) {
	var calls atomic.Int32
	g := generatorAt(t, func(w http.ResponseWriter, r *http.Request) { calls.Add(1) })
	b := sampleBundle(t)
	b.Evidence[0].Text += "changed"
	if _, err := g.Generate(context.Background(), b); err == nil {
		t.Fatal("unsealed evidence accepted")
	}
	if out := Rules(b); len(out.Sections) != 0 || !strings.Contains(out.Summary, "不可用") {
		t.Fatal("rules accepted mutated evidence")
	}
	b = sampleBundle(t)
	b.Evidence[0].SourceURL = strings.Repeat("a", 600*1024)
	if err := b.seal(); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Generate(context.Background(), b); err == nil {
		t.Fatal("oversized payload accepted")
	}
	if calls.Load() != 0 {
		t.Fatal("invalid evidence sent to provider")
	}
}
