package analysis

import (
	"context"
	"cornwatch/internal/domain"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestValidateRejectsFabricatedEvidence(t *testing.T) {
	e := domain.Event{Title: "山东收购价", Body: "企业报价为2200元/吨，水分14%。"}
	a := domain.Analysis{Summary: "报价需核验", Direction: "unknown", Confidence: .2, Reasoning: []string{"口径不完整"}, Evidence: []string{"企业报价为2200元/吨"}}
	if err := Validate(a, e); err != nil {
		t.Fatalf("real evidence rejected: %v", err)
	}
	for _, quote := range []string{"企业报价为2300元/吨", "2200元/吨，水分15%", "", "  "} {
		a.Evidence = []string{quote}
		if err := Validate(a, e); err == nil {
			t.Errorf("fabricated/empty quote accepted: %q", quote)
		}
	}
}

func TestAnalyzeRejectsTruncationAndDoesNotExposeCredential(t *testing.T) {
	const key = "private-secret-not-event-data"
	for _, finish := range []string{"length", "stop"} {
		t.Run(finish, func(t *testing.T) {
			endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer "+key {
					t.Error("wrong model endpoint or authentication")
				}
				data, _ := io.ReadAll(r.Body)
				if strings.Contains(string(data), key) {
					t.Error("credential leaked into model input")
				}
				content := `{"summary":"核实口径","direction":"unknown","confidence":0.1,"reasoning":["原文只提供报价"],"evidence":["报价2200元"],"risk_flags":[]}`
				_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"finish_reason": finish, "message": map[string]string{"content": content}}}})
			}))
			defer endpoint.Close()
			a, err := New(Config{BaseURL: endpoint.URL + "/v1", Model: "test-model", APIKey: key})
			if err != nil {
				t.Fatal(err)
			}
			out, err := a.Analyze(context.Background(), domain.Event{Title: "报价", Body: "报价2200元"})
			if finish == "length" && err == nil {
				t.Fatal("truncated output accepted")
			}
			if finish == "stop" && (err != nil || out.AnalyzedAt.IsZero() || out.Model != "test-model / prompt-v1") {
				t.Fatalf("completed analysis rejected: %+v %v", out, err)
			}
		})
	}
}

func TestModelErrorBodyIsNotReturnedToCaller(t *testing.T) {
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		_, _ = io.WriteString(w, "sensitive provider diagnostics")
	}))
	defer endpoint.Close()
	a, err := New(Config{BaseURL: endpoint.URL, Model: "test"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.Analyze(context.Background(), domain.Event{Title: "报价", Body: "报价2200元"})
	if err == nil || strings.Contains(err.Error(), "sensitive") {
		t.Fatalf("unsafe provider error: %v", err)
	}
}

func TestRemoteModelRequiresHTTPSAndCleanURL(t *testing.T) {
	for _, address := range []string{"http://example.com/v1", "https://user:pass@example.com/v1", "https://example.com/v1?key=secret", "https://example.com/v1#fragment"} {
		if _, err := New(Config{BaseURL: address}); err == nil {
			t.Errorf("unsafe base URL accepted: %s", address)
		}
	}
}
