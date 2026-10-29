package server

import (
	"errors"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"cornwatch/internal/analysis"
	"cornwatch/internal/dailyreport"
)

var reportZone = time.FixedZone("Asia/Shanghai", 8*3600)
var errReportBusy = errors.New("日报任务正在运行，请稍后查看结果")
var errReportConflict = errors.New("报告配置已在其他窗口更新，请刷新后合并修改")

type reportSettings struct {
	Revision  uint64   `json:"revision"`
	BaseURL   string   `json:"base_url"`
	Model     string   `json:"model"`
	APIKey    string   `json:"api_key,omitempty"`
	Enabled   bool     `json:"enabled"`
	PreTime   string   `json:"pre_time"`
	PostTime  string   `json:"post_time"`
	AutoSave  bool     `json:"auto_save"`
	SkipDates []string `json:"skip_dates"`
}
type reportSettingsView struct {
	Revision      uint64   `json:"revision"`
	BaseURL       string   `json:"base_url"`
	Model         string   `json:"model"`
	KeyConfigured bool     `json:"key_configured"`
	Configured    bool     `json:"configured"`
	Enabled       bool     `json:"enabled"`
	PreTime       string   `json:"pre_time"`
	PostTime      string   `json:"post_time"`
	AutoSave      bool     `json:"auto_save"`
	SkipDates     []string `json:"skip_dates"`
	Timezone      string   `json:"timezone"`
	WindowMinutes int      `json:"window_minutes"`
	Source        string   `json:"source"`
}

func (c reportSettings) llm() analysis.Config {
	return analysis.Config{BaseURL: c.BaseURL, Model: c.Model, APIKey: c.APIKey}
}
func (c reportSettings) configured() bool { return c.BaseURL != "" && c.Model != "" }
func (c reportSettings) view(source string) reportSettingsView {
	return reportSettingsView{c.Revision, c.BaseURL, c.Model, c.APIKey != "", c.configured(), c.Enabled, c.PreTime, c.PostTime, c.AutoSave, c.SkipDates, "Asia/Shanghai", 60, source}
}
func (s *Server) loadReportSettings() (reportSettings, string, error) {
	var c reportSettings
	err := s.Store.GetMeta("report_settings_v1", &c)
	if errors.Is(err, os.ErrNotExist) {
		return reportSettings{Revision: 1, BaseURL: s.Analyzer.Config.BaseURL, Model: s.Analyzer.Config.Model, APIKey: s.Analyzer.Config.APIKey, Enabled: true, PreTime: "08:30", PostTime: "15:30", AutoSave: true, SkipDates: []string{}}, "environment", nil
	}
	if err != nil {
		return c, "saved", errors.New("读取报告配置失败，请检查数据库")
	}
	if c.Revision == 0 || validateReportSettings(&c) != nil {
		return c, "saved", errors.New("已保存报告配置无效，请检查备份或恢复配置")
	}
	return c, "saved", nil
}
func validClock(v string) bool {
	t, e := time.Parse("15:04", v)
	return e == nil && t.Format("15:04") == v
}
func validateReportSettings(c *reportSettings) error {
	c.BaseURL = strings.TrimRight(strings.TrimSpace(c.BaseURL), "/")
	c.Model = strings.TrimSpace(c.Model)
	if len(c.BaseURL) > 2048 || len(c.Model) > 200 || len(c.APIKey) > 4096 {
		return errors.New("模型配置超过长度限制")
	}
	if strings.ContainsAny(c.Model, "\r\n") || strings.ContainsAny(c.APIKey, "\r\n\t ") {
		return errors.New("模型名称或凭据含有无效空白字符")
	}
	if _, err := analysis.New(c.llm()); err != nil {
		return errors.New("模型地址必须为无账号、查询参数或片段的HTTPS基础地址；本机模型可使用HTTP")
	}
	if !validClock(c.PreTime) || !validClock(c.PostTime) || c.PreTime >= "09:00" || c.PreTime < "06:00" || c.PostTime < "15:15" || c.PostTime > "19:00" {
		return errors.New("盘前时间须为06:00—08:59，盘后时间须为15:15—19:00（北京时间）")
	}
	if len(c.SkipDates) > 366 {
		return errors.New("最多设置366个排除日期")
	}
	seen := map[string]bool{}
	dates := []string{}
	for _, d := range c.SkipDates {
		t, e := time.Parse("2006-01-02", d)
		if e != nil || t.Format("2006-01-02") != d {
			return errors.New("排除日期须为YYYY-MM-DD")
		}
		if !seen[d] {
			seen[d] = true
			dates = append(dates, d)
		}
	}
	sort.Strings(dates)
	c.SkipDates = dates
	return nil
}
func (s *Server) getReportSettings(w http.ResponseWriter, r *http.Request) {
	s.reportMu.Lock()
	c, source, err := s.loadReportSettings()
	s.reportMu.Unlock()
	if err != nil {
		fail(w, 500, err)
		return
	}
	write(w, 200, c.view(source))
}
func (s *Server) saveReportSettings(w http.ResponseWriter, r *http.Request) {
	var req struct {
		reportSettings
		ClearAPIKey bool `json:"clear_api_key"`
	}
	if err := decode(w, r, &req); err != nil {
		fail(w, 400, err)
		return
	}
	s.reportMu.Lock()
	old, _, err := s.loadReportSettings()
	if err == nil && old.Revision != req.Revision {
		err = errReportConflict
	}
	c := req.reportSettings
	c.BaseURL = strings.TrimRight(strings.TrimSpace(c.BaseURL), "/")
	if err == nil && req.ClearAPIKey && c.APIKey != "" {
		err = errors.New("清除凭据与填写新凭据不能同时使用")
	}
	if err == nil && c.APIKey == "" && !req.ClearAPIKey && c.BaseURL == strings.TrimRight(old.BaseURL, "/") {
		c.APIKey = old.APIKey
	}
	if err == nil {
		err = validateReportSettings(&c)
	}
	if err == nil {
		c.Revision = old.Revision + 1
		err = s.Store.SetMeta("report_settings_v1", c)
	}
	s.reportMu.Unlock()
	if err != nil {
		status := 400
		if errors.Is(err, errReportConflict) {
			status = 409
		}
		fail(w, status, err)
		return
	}
	s.changed("report-settings")
	write(w, 200, c.view("saved"))
}
func (s *Server) testReportSettings(w http.ResponseWriter, r *http.Request) {
	s.reportMu.Lock()
	if s.reportBusy {
		s.reportMu.Unlock()
		fail(w, 429, errReportBusy)
		return
	}
	c, _, err := s.loadReportSettings()
	if err == nil && !c.configured() {
		err = errors.New("请先保存模型地址与模型名称")
	}
	if err != nil {
		s.reportMu.Unlock()
		fail(w, 400, err)
		return
	}
	s.reportBusy = true
	s.reportMu.Unlock()
	defer func() { s.reportMu.Lock(); s.reportBusy = false; s.reportMu.Unlock() }()
	g, err := dailyreport.New(c.llm())
	if err == nil {
		err = g.Test(r.Context())
	}
	if err != nil {
		fail(w, 400, errors.New(safeReportError(err, c.APIKey)))
		return
	}
	write(w, 200, map[string]any{"ok": true, "message": "模型连接与结构化引用校验通过；使用连接示例，未保存市场报告。"})
}
