// Package industry reads a small, bounded set of public official reports.
// These are delayed statistical observations, never exchange quotes or orders.
package industry

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"cornwatch/internal/domain"
	"cornwatch/internal/store"
	"golang.org/x/net/html"
)

const (
	LiaoningURL         = "https://lcj.ln.gov.cn/lswzcb/xwdt24/tjxx/index.shtml"
	MOAFeedURL          = "https://xmsyj.moa.gov.cn/jcyj/"
	RecommendedInterval = 6 * time.Hour
	maxBodyBytes        = 2 * 1024 * 1024
)

var shanghai = time.FixedZone("Asia/Shanghai", 8*3600)
var liaoningPath = regexp.MustCompile(`^/lswzcb/xwdt24/tjxx/[0-9]{19}/index\.shtml$`)
var moaPath = regexp.MustCompile(`^/jcyj/[0-9]{6}/t[0-9]{8}_[0-9]+\.htm$`)
var liaoningTitle = regexp.MustCompile(`^辽宁省粮油价格监测报表[0-9]{4}年第[0-9]{1,3}期$`)
var moaTitle = regexp.MustCompile(`^[0-9]{1,2}月第[0-9]周畜产品和饲料集贸市场价格情况$`)
var moaListTitle = regexp.MustCompile(`^[0-9]{1,2}月第[0-9]周畜产品和饲料集贸市场价格情况(?:[0-9]{4}-[0-9]{2}-[0-9]{2})?$`)
var monitoringDate = regexp.MustCompile(`监测时间[：:]([0-9]{4})年([0-9]{1,2})月([0-9]{1,2})日`)
var feedDate = regexp.MustCompile(`采集日为([0-9]{1,2})月([0-9]{1,2})日`)
var pricePattern = regexp.MustCompile(`^(?:[0-9]+|[0-9]{1,3}(?:,[0-9]{3})+)(?:\.[0-9]+)?$`)

type Collector struct {
	Client *http.Client
	// ReportsPerSource is bounded to 1..3; zero selects three reports.
	ReportsPerSource int
	RequestSpacing   time.Duration
	now              func() time.Time
}

func New() *Collector {
	return &Collector{Client: &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) > 2 || len(via) == 0 || req.URL.Host != via[0].URL.Host || !allowedURL(req.URL.String()) {
			return errors.New("官方数据源不允许跨域或非HTTPS重定向")
		}
		return nil
	}}, ReportsPerSource: 3, RequestSpacing: 250 * time.Millisecond, now: time.Now}
}

func (c *Collector) Liaoning(ctx context.Context) ([]domain.Observation, []domain.Event, error) {
	return c.collect(ctx, LiaoningURL, ParseLiaoningList, ParseLiaoning)
}
func (c *Collector) MOAFeed(ctx context.Context) ([]domain.Observation, []domain.Event, error) {
	return c.collect(ctx, MOAFeedURL, ParseMOAList, ParseMOAFeed)
}

type ReportLink struct {
	Title string `json:"title"`
	URL   string `json:"url"`
}
type listParser func([]byte) ([]ReportLink, error)
type detailParser func([]byte, string, time.Time) ([]domain.Observation, []domain.Event, error)

// A source is atomic at collection level: if any selected report fails, no
// partial records are returned as a successful update. The hub schedules the
// two sources separately, so failure of one does not hide the other's status.
func (c *Collector) collect(ctx context.Context, listURL string, parseList listParser, parseDetail detailParser) ([]domain.Observation, []domain.Event, error) {
	limit := c.ReportsPerSource
	if limit == 0 {
		limit = 3
	}
	if limit < 1 || limit > 3 {
		return nil, nil, errors.New("每个官方源每次只能读取最新1至3期")
	}
	data, err := c.get(ctx, listURL)
	if err != nil {
		return nil, nil, err
	}
	links, err := parseList(data)
	if err != nil {
		return nil, nil, err
	}
	if len(links) > limit {
		links = links[:limit]
	}
	observations := []domain.Observation{}
	events := []domain.Event{}
	for _, link := range links {
		if c.RequestSpacing > 0 {
			timer := time.NewTimer(c.RequestSpacing)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, nil, ctx.Err()
			case <-timer.C:
			}
		}
		data, err = c.get(ctx, link.URL)
		if err != nil {
			return nil, nil, fmt.Errorf("%s：%w", link.Title, err)
		}
		now := time.Now().UTC()
		if c.now != nil {
			now = c.now().UTC()
		}
		obs, ev, err := parseDetail(data, link.URL, now)
		if err != nil {
			return nil, nil, fmt.Errorf("%s：%w", link.Title, err)
		}
		observations = append(observations, obs...)
		events = append(events, ev...)
	}
	return observations, events, nil
}

func allowedURL(address string) bool {
	u, e := url.Parse(address)
	if e != nil || u.Scheme != "https" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	return (u.Host == "lcj.ln.gov.cn" && (u.String() == LiaoningURL || liaoningPath.MatchString(u.Path))) || (u.Host == "xmsyj.moa.gov.cn" && (u.String() == MOAFeedURL || moaPath.MatchString(u.Path)))
}
func (c *Collector) get(ctx context.Context, address string) ([]byte, error) {
	if !allowedURL(address) {
		return nil, errors.New("采集地址不在官方公开报告白名单中")
	}
	if c.Client == nil {
		return nil, errors.New("官方数据源HTTP客户端未配置")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "CornWatch/0.3 public-report research (low-frequency)")
	req.Header.Set("Accept", "text/html")
	resp, err := c.Client.Do(req)
	if err != nil {
		return nil, errors.New("官方数据源网络错误或超时")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("官方数据源返回HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes+1))
	if err != nil || len(data) > maxBodyBytes {
		return nil, errors.New("官方报告过大或读取失败")
	}
	if !utf8.Valid(data) {
		return nil, errors.New("官方报告编码变化，暂停解析以免读取错误数值")
	}
	return data, nil
}

func parseHTML(data []byte) (*html.Node, error) {
	if len(data) == 0 || len(data) > maxBodyBytes || !utf8.Valid(data) {
		return nil, errors.New("报告HTML为空、过大或编码无效")
	}
	return html.Parse(bytes.NewReader(data))
}
func attr(n *html.Node, name string) string {
	for _, a := range n.Attr {
		if a.Key == name {
			return a.Val
		}
	}
	return ""
}
func walk(n *html.Node, visit func(*html.Node)) {
	visit(n)
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		walk(c, visit)
	}
}
func find(n *html.Node, predicate func(*html.Node) bool) *html.Node {
	if predicate(n) {
		return n
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if found := find(c, predicate); found != nil {
			return found
		}
	}
	return nil
}
func nodeText(n *html.Node) string {
	if n.Type == html.TextNode {
		return n.Data
	}
	if n.Type == html.ElementNode && (n.Data == "script" || n.Data == "style" || n.Data == "noscript" || n.Data == "template") {
		return ""
	}
	var b strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		b.WriteString(nodeText(c))
	}
	return b.String()
}
func compact(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) || r == '\u200b' || r == '\ufeff' {
			return -1
		}
		return r
	}, s)
}
func meta(doc *html.Node, name string) string {
	n := find(doc, func(n *html.Node) bool {
		return n.Type == html.ElementNode && n.Data == "meta" && strings.EqualFold(attr(n, "name"), name)
	})
	if n == nil {
		return ""
	}
	return strings.TrimSpace(attr(n, "content"))
}
func parseList(data []byte, baseURL string, titlePattern *regexp.Regexp) ([]ReportLink, error) {
	doc, err := parseHTML(data)
	if err != nil {
		return nil, err
	}
	base, _ := url.Parse(baseURL)
	links := []ReportLink{}
	seen := map[string]bool{}
	walk(doc, func(n *html.Node) {
		if n.Type != html.ElementNode || n.Data != "a" {
			return
		}
		title := compact(nodeText(n))
		if !titlePattern.MatchString(title) {
			return
		}
		u, e := url.Parse(strings.TrimSpace(attr(n, "href")))
		if e != nil {
			return
		}
		u = base.ResolveReference(u)
		if !allowedURL(u.String()) || u.Host != base.Host || seen[u.String()] || u.String() == baseURL {
			return
		}
		seen[u.String()] = true
		links = append(links, ReportLink{Title: title, URL: u.String()})
	})
	if len(links) == 0 {
		return nil, errors.New("官方列表未找到约定报告，页面可能改版；未生成占位数据")
	}
	return links, nil
}
func ParseLiaoningList(data []byte) ([]ReportLink, error) {
	return parseList(data, LiaoningURL, liaoningTitle)
}
func ParseMOAList(data []byte) ([]ReportLink, error) {
	return parseList(data, MOAFeedURL, moaListTitle)
}

func publication(doc *html.Node, now time.Time) (time.Time, bool, error) {
	value := meta(doc, "PubDate")
	for _, layout := range []string{"2006-01-02 15:04:05", "2006-01-02"} {
		if t, err := time.ParseInLocation(layout, value, shanghai); err == nil {
			if t.After(now.Add(5 * time.Minute)) {
				return time.Time{}, false, errors.New("报告发布日期在未来")
			}
			return t.UTC(), layout == "2006-01-02", nil
		}
	}
	return time.Time{}, false, errors.New("报告缺少可信发布日期")
}
func dateFromParts(year, month, day string) (time.Time, error) {
	y, e1 := strconv.Atoi(year)
	m, e2 := strconv.Atoi(month)
	d, e3 := strconv.Atoi(day)
	if e1 != nil || e2 != nil || e3 != nil {
		return time.Time{}, errors.New("监测日期无效")
	}
	t := time.Date(y, time.Month(m), d, 0, 0, 0, 0, shanghai)
	if t.Year() != y || int(t.Month()) != m || t.Day() != d {
		return time.Time{}, errors.New("监测日期无效")
	}
	return t.UTC(), nil
}
func price(s string, min, max float64) (float64, error) {
	s = compact(s)
	if !pricePattern.MatchString(s) {
		return 0, errors.New("价格不是明确单一数值")
	}
	v, e := strconv.ParseFloat(strings.ReplaceAll(s, ",", ""), 64)
	if e != nil || v < min || v > max {
		return 0, errors.New("价格缺失、范围或单位异常，暂停采集")
	}
	return v, nil
}
func makeObservation(source, address, kind, region, entity string, value float64, unit string, observed, now time.Time, note string) domain.Observation {
	return domain.Observation{ID: store.ID("industry-public-v1", source, address, entity, observed.Format(time.RFC3339), strconv.FormatFloat(value, 'f', -1, 64), unit), Kind: kind, Region: region, Entity: entity, Value: value, Unit: unit, ObservedAt: observed, AvailableAt: now, Source: source, SourceURL: address, Note: note, IsDemo: false}
}
func makeEvent(source, address, title, body, region, category string, products []string, published, now time.Time) domain.Event {
	return domain.Event{ID: store.ID("industry-public-v1", source, address, title, body, published.Format(time.RFC3339)), Title: title, Body: body, Category: category, Region: region, Products: products, Source: source, SourceURL: address, PublishedAt: published, IngestedAt: now, AvailableAt: now, Severity: "low", IsDemo: false}
}

func ParseLiaoning(data []byte, address string, now time.Time) ([]domain.Observation, []domain.Event, error) {
	if !allowedURL(address) || address == LiaoningURL || !strings.HasPrefix(address, "https://lcj.ln.gov.cn/") {
		return nil, nil, errors.New("辽宁报告链接无效")
	}
	doc, err := parseHTML(data)
	if err != nil {
		return nil, nil, err
	}
	title := compact(meta(doc, "ArticleTitle"))
	if !liaoningTitle.MatchString(title) {
		return nil, nil, errors.New("辽宁报告标题不匹配")
	}
	published, dateOnly, err := publication(doc, now)
	if err != nil {
		return nil, nil, err
	}
	body := find(doc, func(n *html.Node) bool { return attr(n, "id") == "UCAP-CONTENT" })
	if body == nil {
		return nil, nil, errors.New("辽宁报告正文结构变化")
	}
	text := compact(nodeText(body))
	matches := monitoringDate.FindAllStringSubmatch(text, -1)
	if len(matches) != 1 || !strings.Contains(text, "主要粮油品种价格为全省14市平均价") {
		return nil, nil, errors.New("辽宁报告缺少唯一监测日期或全省14市平均价口径")
	}
	observed, err := dateFromParts(matches[0][1], matches[0][2], matches[0][3])
	if err != nil || observed.After(published) {
		return nil, nil, errors.New("辽宁报告监测日期无效或晚于发布日期")
	}
	var rows [][]string
	walk(body, func(n *html.Node) {
		if n.Type != html.ElementNode || n.Data != "tr" {
			return
		}
		cells := []string{}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if c.Type == html.ElementNode && (c.Data == "td" || c.Data == "th") {
				cells = append(cells, compact(nodeText(c)))
			}
		}
		if len(cells) > 0 {
			rows = append(rows, cells)
		}
	})
	if len(rows) < 2 || len(rows[0]) != 7 || rows[0][0] != "品种" || rows[0][1] != "收购价格（元/吨）" || rows[0][2] != "周环比" || rows[0][3] != "进厂价格" || rows[0][4] != "周环比" {
		return nil, nil, errors.New("辽宁报告表头、单位或列顺序变化，未猜测价格")
	}
	var corn []string
	for _, row := range rows[1:] {
		if len(row) > 0 && row[0] == "玉米" {
			if corn != nil {
				return nil, nil, errors.New("辽宁报告玉米行不唯一")
			}
			corn = row
		}
	}
	if len(corn) != 7 {
		return nil, nil, errors.New("辽宁报告缺少约定玉米数据行")
	}
	purchase, e1 := price(corn[1], 100, 10000)
	factory, e2 := price(corn[3], 100, 10000)
	if e1 != nil || e2 != nil {
		return nil, nil, errors.New("辽宁玉米价格不是有效元/吨数值")
	}
	note := fmt.Sprintf("官方周度监测；全省14市平均价，不是单个工厂当日报价。监测日期%s，发布日期%s；具体水分、等级、税费及样本权重未在本表给出，不能直接作为期货可交割基差。进厂价格沿用原表元/吨口径。", observed.In(shanghai).Format("2006-01-02"), published.In(shanghai).Format("2006-01-02"))
	if dateOnly {
		note += "原页仅提供发布日期，published_at以当日00:00表示日期；首次可用时间以available_at为准。"
	}
	source := "辽宁省粮食和物资储备局"
	obs := []domain.Observation{makeObservation(source, address, "purchase_price", "辽宁·全省14市", "玉米收购均价（全省14市）", purchase, "元/吨", observed, now, note), makeObservation(source, address, "purchase_price", "辽宁·全省14市", "玉米进厂均价（全省14市）", factory, "元/吨", observed, now, note)}
	eventBody := fmt.Sprintf("监测日期%s；辽宁全省14市玉米收购均价%.2f元/吨、进厂均价%.2f元/吨。%s", observed.In(shanghai).Format("2006-01-02"), purchase, factory, note)
	return obs, []domain.Event{makeEvent(source, address, title, eventBody, "辽宁", "purchase_price", []string{"c"}, published, now)}, nil
}

func ParseMOAFeed(data []byte, address string, now time.Time) ([]domain.Observation, []domain.Event, error) {
	if !allowedURL(address) || address == MOAFeedURL || !strings.HasPrefix(address, "https://xmsyj.moa.gov.cn/") {
		return nil, nil, errors.New("农业农村部报告链接无效")
	}
	doc, err := parseHTML(data)
	if err != nil {
		return nil, nil, err
	}
	title := compact(meta(doc, "ArticleTitle"))
	if !moaTitle.MatchString(title) {
		return nil, nil, errors.New("农业农村部报告标题不匹配")
	}
	published, dateOnly, err := publication(doc, now)
	if err != nil {
		return nil, nil, err
	}
	body := find(doc, func(n *html.Node) bool {
		for _, class := range strings.Fields(attr(n, "class")) {
			if class == "TRS_Editor" {
				return true
			}
		}
		return false
	})
	if body == nil {
		return nil, nil, errors.New("农业农村部报告正文结构变化")
	}
	text := compact(nodeText(body))
	dates := feedDate.FindAllStringSubmatch(text, -1)
	if len(dates) != 1 || !strings.Contains(text, "全国500个县集贸市场和采集点") {
		return nil, nil, errors.New("报告缺少唯一采集日期或全国500县样本口径")
	}
	year := published.In(shanghai).Year()
	observed, err := dateFromParts(strconv.Itoa(year), dates[0][1], dates[0][2])
	if err == nil && observed.After(published) && published.In(shanghai).Month() == time.January && dates[0][1] == "12" {
		observed, err = dateFromParts(strconv.Itoa(year-1), dates[0][1], dates[0][2])
	}
	if err != nil || observed.After(published) || published.Sub(observed) > 31*24*time.Hour {
		return nil, nil, errors.New("周报采集日期无效、在未来或距发布超过31天")
	}
	source := "农业农村部畜牧兽医局"
	note := fmt.Sprintf("全国500个县集贸市场和采集点周度监测；采集日期%s，发布日期%s。为全国样本集贸市场均价，非工厂收购价、非生猪期货交割现货、非逐笔成交价；各品种保持原页元/公斤单位，不拼接其他统计口径。", observed.In(shanghai).Format("2006-01-02"), published.In(shanghai).Format("2006-01-02"))
	if dateOnly {
		note += "原页仅提供发布日期，published_at当日00:00只表示日期。"
	}
	obs := []domain.Observation{}
	facts := []string{}
	for _, metric := range []struct {
		name     string
		min, max float64
	}{{"玉米", .1, 20}, {"生猪", 1, 200}, {"豆粕", .1, 50}} {
		pattern := regexp.MustCompile(`全国` + metric.name + `平均价格([0-9]+(?:\.[0-9]+)?)元/公斤`)
		matches := pattern.FindAllStringSubmatch(text, -1)
		if len(matches) != 1 {
			return nil, nil, fmt.Errorf("全国%s价格缺失、不唯一或单位变化", metric.name)
		}
		value, err := price(matches[0][1], metric.min, metric.max)
		if err != nil {
			return nil, nil, fmt.Errorf("全国%s价格异常", metric.name)
		}
		obs = append(obs, makeObservation(source, address, "related_price", "全国·500县监测", "全国"+metric.name+"集贸市场均价", value, "元/公斤", observed, now, note))
		facts = append(facts, fmt.Sprintf("%s%.2f元/公斤", metric.name, value))
	}
	eventBody := fmt.Sprintf("采集日期%s；全国样本集贸市场均价：%s。%s", observed.In(shanghai).Format("2006-01-02"), strings.Join(facts, "、"), note)
	return obs, []domain.Event{makeEvent(source, address, title, eventBody, "全国", "related_price", []string{"c", "lh", "m"}, published, now)}, nil
}
