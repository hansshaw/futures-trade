// Package industrypublic reads delayed, public industry reports. It never
// guesses a missing unit, requests member content, or creates executable prices.
package industrypublic

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
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
	ListURL             = "https://www.boyar.cn/column/7.html"
	RecommendedInterval = 2 * time.Hour
	MissingUnit         = "未注明"
	maxBody             = 2 * 1024 * 1024
)

var china = time.FixedZone("Asia/Shanghai", 8*3600)
var articlePath = regexp.MustCompile(`^/article/[1-9][0-9]{5,8}\.html$`)
var titlePattern = regexp.MustCompile(`^([0-9]{4})年([0-9]{1,2})月([0-9]{1,2})日(国内部分玉米深加工企业报价|国内部分玉米深加工企业门到货车辆|吉林燃料乙醇公司门前玉米收购通知)$`)
var pubPattern = regexp.MustCompile(`^发布时间：([0-9]{4}-[0-9]{2}-[0-9]{2} [0-9]{2}:[0-9]{2})来源：(.+)$`)
var number = regexp.MustCompile(`^-?[0-9]+(?:\.[0-9]+)?$`)

type Collector struct {
	Client *http.Client
	// ReportsPerSource is bounded to 1..3 across price tables and supported
	// notices together; zero means three. No pagination or archive enumeration.
	ReportsPerSource int
	RequestSpacing   time.Duration
	now              func() time.Time
}

func New() *Collector {
	return &Collector{Client: &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) > 2 || !allowedURL(req.URL.String()) {
			return errors.New("公开报告禁止跳转到登录页、跨域或白名单外地址")
		}
		return nil
	}}, ReportsPerSource: 3, RequestSpacing: 500 * time.Millisecond, now: time.Now}
}
func (c *Collector) Prices(ctx context.Context) ([]domain.Observation, []domain.Event, error) {
	return c.collect(ctx, false)
}
func (c *Collector) Arrivals(ctx context.Context) ([]domain.Observation, []domain.Event, error) {
	return c.collect(ctx, true)
}

type ReportLink struct {
	Title string
	URL   string
	Kind  string
	Date  time.Time
}

func allowedURL(address string) bool {
	u, err := url.Parse(address)
	return err == nil && u.Scheme == "https" && u.Host == "www.boyar.cn" && u.User == nil && u.RawQuery == "" && u.Fragment == "" && (address == ListURL || articlePath.MatchString(u.Path))
}
func (c *Collector) get(ctx context.Context, address string) ([]byte, error) {
	if !allowedURL(address) || c.Client == nil {
		return nil, errors.New("公开报告地址或HTTP客户端无效")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "CornWatch/0.4 public-report research (low-frequency)")
	req.Header.Set("Accept", "text/html")
	resp, err := c.Client.Do(req)
	if err != nil {
		return nil, errors.New("公开报告连接失败、登录跳转被拒或超时")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("公开报告返回HTTP %d；不尝试绕过访问限制", resp.StatusCode)
	}
	// Custom transports must not silently replace an approved URL with a login
	// page or a different host either.
	if resp.Request != nil && !allowedURL(resp.Request.URL.String()) {
		return nil, errors.New("响应已跳转出公开报告白名单")
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil || len(data) > maxBody || !utf8.Valid(data) {
		return nil, errors.New("报告读取失败、过大或UTF-8编码发生变化")
	}
	return data, nil
}
func (c *Collector) collect(ctx context.Context, arrivals bool) ([]domain.Observation, []domain.Event, error) {
	limit := c.ReportsPerSource
	if limit == 0 {
		limit = 3
	}
	if limit < 1 || limit > 3 {
		return nil, nil, errors.New("每种报告每次只允许读取最新1至3篇")
	}
	data, err := c.get(ctx, ListURL)
	if err != nil {
		return nil, nil, err
	}
	links, err := ParseList(data)
	if err != nil {
		return nil, nil, err
	}
	observations := []domain.Observation{}
	events := []domain.Event{}
	seen := map[string]bool{}
	selected := 0
	for _, link := range links {
		if (link.Kind == "arrivals") != arrivals || selected >= limit {
			continue
		}
		selected++
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
		obs, ev, parseErr := ParseReport(data, link.URL, now)
		if parseErr != nil {
			return nil, nil, fmt.Errorf("%s：%w", link.Title, parseErr)
		}
		if len(ev) != 1 || (ev[0].Title != link.Title && ev[0].Title != "企业收购公告｜"+link.Title) {
			return nil, nil, errors.New("报告正文标题与栏目链接不一致，未混合错误页面")
		}
		for _, o := range obs {
			if !seen[o.ID] {
				observations = append(observations, o)
				seen[o.ID] = true
			}
		}
		for _, e := range ev {
			if !seen[e.ID] {
				events = append(events, e)
				seen[e.ID] = true
			}
		}
	}
	if selected == 0 {
		return nil, nil, errors.New("公开栏目当前没有匹配的报告；未回退到热力排行或伪造最新数据")
	}
	return observations, events, nil
}
func doc(data []byte) (*html.Node, error) {
	if len(data) == 0 || len(data) > maxBody || !utf8.Valid(data) {
		return nil, errors.New("报告为空、过大或编码无效")
	}
	return html.Parse(bytes.NewReader(data))
}
func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}
func hasClass(n *html.Node, class string) bool {
	for _, v := range strings.Fields(attr(n, "class")) {
		if v == class {
			return true
		}
	}
	return false
}
func walk(n *html.Node, f func(*html.Node)) {
	f(n)
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		walk(child, f)
	}
}
func find(n *html.Node, f func(*html.Node) bool) *html.Node {
	if f(n) {
		return n
	}
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		if v := find(child, f); v != nil {
			return v
		}
	}
	return nil
}
func text(n *html.Node) string {
	if n.Type == html.TextNode {
		return n.Data
	}
	if n.Type == html.CommentNode {
		return ""
	}
	if n.Data == "script" || n.Data == "style" || n.Data == "noscript" || n.Data == "template" || strings.Contains(strings.ReplaceAll(attr(n, "style"), " ", ""), "display:none") {
		return ""
	}
	var b strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		b.WriteString(text(c))
	}
	return b.String()
}
func compact(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) || r == '\ufeff' || r == '\u200b' {
			return -1
		}
		return r
	}, s)
}
func parseTitle(s string) (string, time.Time, error) {
	m := titlePattern.FindStringSubmatch(compact(s))
	if len(m) != 5 {
		return "", time.Time{}, errors.New("报告标题不符合约定")
	}
	y, _ := strconv.Atoi(m[1])
	month, _ := strconv.Atoi(m[2])
	day, _ := strconv.Atoi(m[3])
	date := time.Date(y, time.Month(month), day, 0, 0, 0, 0, china)
	if date.Year() != y || int(date.Month()) != month || date.Day() != day {
		return "", time.Time{}, errors.New("报告日期无效")
	}
	kind := "prices"
	if strings.HasSuffix(m[4], "车辆") {
		kind = "arrivals"
	}
	if strings.HasSuffix(m[4], "通知") {
		kind = "notice"
	}
	return kind, date.UTC(), nil
}
func ParseList(data []byte) ([]ReportLink, error) {
	root, err := doc(data)
	if err != nil {
		return nil, err
	}
	body := find(root, func(n *html.Node) bool { return hasClass(n, "conntent_left_right") })
	if body == nil {
		return nil, errors.New("玉米栏目正文结构变化或需登录")
	}
	out := []ReportLink{}
	seen := map[string]bool{}
	base, _ := url.Parse(ListURL)
	walk(body, func(n *html.Node) {
		if n.Data != "a" || n.Type != html.ElementNode {
			return
		}
		title := compact(text(n))
		kind, date, e := parseTitle(title)
		if e != nil {
			return
		}
		// Red titles are explicitly member-only in the publisher's own HTML.
		if find(n, func(v *html.Node) bool {
			return strings.Contains(compact(strings.ToLower(attr(v, "style"))), "color:red")
		}) != nil {
			return
		}
		u, e := url.Parse(attr(n, "href"))
		if e != nil {
			return
		}
		address := base.ResolveReference(u).String()
		if !allowedURL(address) || address == ListURL || seen[address] {
			return
		}
		seen[address] = true
		out = append(out, ReportLink{title, address, kind, date})
	})
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Date.Equal(out[j].Date) {
			return out[i].URL > out[j].URL
		}
		return out[i].Date.After(out[j].Date)
	})
	if len(out) == 0 {
		return nil, errors.New("公开玉米栏目没有匹配报告，会员标题和热力排行未采集")
	}
	return out, nil
}
func ParseReport(data []byte, address string, now time.Time) ([]domain.Observation, []domain.Event, error) {
	if !allowedURL(address) || address == ListURL {
		return nil, nil, errors.New("报告URL不在白名单")
	}
	root, err := doc(data)
	if err != nil {
		return nil, nil, err
	}
	titleNode := find(root, func(n *html.Node) bool { return hasClass(n, "fs32") && hasClass(n, "tac") })
	pubNode := find(root, func(n *html.Node) bool { return hasClass(n, "tac") && hasClass(n, "c8") && hasClass(n, "fs16") })
	body := find(root, func(n *html.Node) bool { return hasClass(n, "fs18") && hasClass(n, "lh36") && hasClass(n, "mt50") })
	if titleNode == nil || pubNode == nil || body == nil {
		return nil, nil, errors.New("报告缺少标题、发布信息或公开正文；可能改版或需登录")
	}
	title := compact(text(titleNode))
	kind, date, err := parseTitle(title)
	if err != nil {
		return nil, nil, err
	}
	publication := strings.Join(strings.Fields(text(pubNode)), " ")
	publication = strings.ReplaceAll(publication, " 来源", "来源")
	m := pubPattern.FindStringSubmatch(strings.TrimSpace(publication))
	if len(m) != 3 {
		return nil, nil, errors.New("报告发布时间/原始来源缺失或格式变化")
	}
	published, err := time.ParseInLocation("2006-01-02 15:04", m[1], china)
	if err != nil || published.After(now.Add(5*time.Minute)) {
		return nil, nil, errors.New("报告发布时间无效或在未来")
	}
	originalSource := strings.TrimSpace(m[2])
	if originalSource != "博亚和讯" && originalSource != "吉林燃料乙醇" {
		return nil, nil, errors.New("报告原始来源发生变化，请先人工核验")
	}
	if date.After(published) && kind != "notice" {
		return nil, nil, errors.New("观测日期晚于发布日期")
	}
	btext := compact(text(body))
	if strings.Contains(btext, "登录后") || strings.Contains(btext, "会员专享") || strings.Contains(btext, "付费查看") {
		return nil, nil, errors.New("正文显示访问限制，不采集会员内容")
	}
	source := "博亚和讯（公开延迟报告；原始来源：" + originalSource + "）"
	if kind == "notice" {
		return parseNotice(body, address, title, source, date, published.UTC(), now)
	}
	if kind == "arrivals" {
		return parseArrivals(btext, address, title, source, date, published.UTC(), now)
	}
	return parsePrices(body, address, title, source, date, published.UTC(), now)
}
func value(s string, min, max float64) (float64, error) {
	if !number.MatchString(s) {
		return 0, errors.New("数值缺失、区间或格式变化")
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || v < min || v > max {
		return 0, errors.New("数值超出有效范围")
	}
	return v, nil
}
func observation(address, source, kind, region, entity string, v float64, unit string, date, now time.Time, note string) domain.Observation {
	return domain.Observation{ID: store.ID("public-enterprise-v1", address, kind, entity, date.UTC().Format(time.RFC3339), strconv.FormatFloat(v, 'g', -1, 64), unit, note), Kind: kind, Region: region, Entity: entity, Value: v, Unit: unit, ObservedAt: date, AvailableAt: now, Source: source, SourceURL: address, Note: note}
}
func event(address, title, source, category, region, body string, published, now time.Time) domain.Event {
	return domain.Event{ID: store.ID("public-enterprise-event-v1", address, title, body, published.Format(time.RFC3339)), Title: title, Body: body, Category: category, Region: region, Products: []string{"c"}, Source: source, SourceURL: address, PublishedAt: published, AvailableAt: now, IngestedAt: now, Severity: "low"}
}
func timingNote(date, published time.Time) string {
	return fmt.Sprintf("原报告日期%s；网页发布%s（北京时间），不是实时交易报价；日期精度仅到日的observed_at以00:00表示，首次系统可用时间见available_at。", date.In(china).Format("2006-01-02"), published.In(china).Format("2006-01-02 15:04"))
}
func parseArrivals(body, address, title, source string, date, published, now time.Time) ([]domain.Observation, []domain.Event, error) {
	pattern := regexp.MustCompile(`([0-9]{1,2})月([0-9]{1,2})日[，,]山东深加工企业晨间到车辆([0-9]+)车[。.]`)
	matches := pattern.FindAllStringSubmatch(body, -1)
	if len(matches) != 1 {
		return nil, nil, errors.New("缺少唯一的山东晨间到车原文；不将其他到货/待卸口径混入")
	}
	m := matches[0]
	month, _ := strconv.Atoi(m[1])
	day, _ := strconv.Atoi(m[2])
	if month != int(date.In(china).Month()) || day != date.In(china).Day() {
		return nil, nil, errors.New("到车正文日期与标题不同")
	}
	n, err := value(m[3], 0, 100000)
	if err != nil {
		return nil, nil, err
	}
	note := timingNote(date, published) + "原文指标为‘山东深加工企业晨间到车辆’，单位车，标准化为辆；未披露具体采样时刻、企业名单、样本数，也未区分晨间新增到厂与门前剩余待卸；不是全省全天累计，不转换成吨数。公开转载数据，原始调查机构未注明。"
	o := observation(address, source, "truck_arrivals", "山东·深加工样本", "山东深加工晨间到车（原页口径未细分）", n, "辆", date, now, note)
	e := event(address, title, source, "truck_arrivals", o.Region, fmt.Sprintf("山东深加工晨间到车 %.0f 辆。%s", n, note), published, now)
	return []domain.Observation{o}, []domain.Event{e}, nil
}
func parsePrices(body *html.Node, address, title, source string, date, published, now time.Time) ([]domain.Observation, []domain.Event, error) {
	tables := []*html.Node{}
	walk(body, func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "table" {
			tables = append(tables, n)
		}
	})
	if len(tables) != 1 {
		return nil, nil, errors.New("企业报价需为唯一公开HTML表格")
	}
	rows := [][]string{}
	walk(tables[0], func(n *html.Node) {
		if n.Data != "tr" {
			return
		}
		cells := []string{}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if c.Type == html.ElementNode && (c.Data == "td" || c.Data == "th") {
				if attr(c, "rowspan") != "" && attr(c, "rowspan") != "1" || attr(c, "colspan") != "" && attr(c, "colspan") != "1" {
					cells = nil
					break
				}
				cells = append(cells, compact(text(c)))
			}
		}
		rows = append(rows, cells)
	})
	if len(rows) < 2 || len(rows) > 101 || len(rows[0]) != 4 || rows[0][0] != "企业名称" || rows[0][3] != "涨跌" {
		return nil, nil, errors.New("企业报价表头、行数或列结构变化")
	}
	previous, e1 := time.ParseInLocation("2006/1/2", rows[0][1], china)
	current, e2 := time.ParseInLocation("2006/1/2", rows[0][2], china)
	if e1 != nil || e2 != nil || !current.Equal(date) || !previous.Before(current) || current.Sub(previous) > 31*24*time.Hour {
		return nil, nil, errors.New("企业报价表格日期与标题不符")
	}
	unit := MissingUnit
	if strings.Contains(compact(text(body)), "单位：元/吨") {
		unit = "元/吨"
	}
	note := timingNote(date, published) + "公开企业报价汇总；原页未披露水分、容重、霉变、干潮粮、票据税费及扣量条件；企业名称沿用原文，地区未从名称猜测。不可视为可交割或可成交价。"
	if unit == MissingUnit {
		note += "原表没有单位标注，仅存原始数值；禁止按数值大小推断元/吨或元/斤，禁止换算、基差及交易信号使用。"
	}
	obs := []domain.Observation{}
	seen := map[string]bool{}
	for _, row := range rows[1:] {
		if len(row) != 4 || row[0] == "" || len(row[0]) > 160 || seen[row[0]] {
			return nil, nil, errors.New("企业行缺失、重复或结构变化")
		}
		seen[row[0]] = true
		old, er1 := value(row[1], 1, 100000)
		v, er2 := value(row[2], 1, 100000)
		change, er3 := value(row[3], -100000, 100000)
		if er1 != nil || er2 != nil || er3 != nil || v-old-change > 0.011 || v-old-change < -0.011 {
			return nil, nil, errors.New("企业报价数值或环比校验失败")
		}
		obs = append(obs, observation(address, source, "purchase_price", "原页未标地区", row[0]+" · 玉米公开参考报价", v, unit, date, now, note))
	}
	e := event(address, title, source, "purchase_price", "全国·公开企业样本", fmt.Sprintf("公开企业报价表共%d家；单位：%s。%s", len(obs), unit, note), published, now)
	return obs, []domain.Event{e}, nil
}
func parseNotice(body *html.Node, address, title, source string, date, published, now time.Time) ([]domain.Observation, []domain.Event, error) {
	s := compact(text(body))
	s = strings.NewReplacer("＜", "<", "＞", ">", "：", ":", "“", "", "”", "").Replace(s)
	effectivePattern := regexp.MustCompile(`吉林燃料乙醇公司定于([0-9]{4})年([0-9]{1,2})月([0-9]{1,2})日([0-9]{1,2}):([0-9]{2})时起`)
	matches := effectivePattern.FindAllStringSubmatch(s, -1)
	if len(matches) != 1 {
		return nil, nil, errors.New("企业通知缺少唯一生效时刻")
	}
	m := matches[0]
	effective, err := time.ParseInLocation("2006-1-2 15:04", m[1]+"-"+m[2]+"-"+m[3]+" "+m[4]+":"+m[5], china)
	if err != nil || effective.In(china).Format("2006-01-02") != date.In(china).Format("2006-01-02") || effective.Sub(published) > 7*24*time.Hour || published.Sub(effective) > 31*24*time.Hour {
		return nil, nil, errors.New("企业通知生效日期无效或超出合理公告范围")
	}
	note := fmt.Sprintf("吉林燃料乙醇公司5号门收购通知转载；发布%s，生效%s（北京时间）。保留新粮、折标和原水分档，不冒充统一干粮基准；筛混不返杂。公开通知不能保证当前仍在执行；以企业后续通知及实际结算为准。首次系统可用时间见available_at。", published.In(china).Format("2006-01-02 15:04"), effective.In(china).Format("2006-01-02 15:04"))
	if !strings.Contains(s, "吉林燃料乙醇公司5号门") || !strings.Contains(s, "筛混不返杂") || !strings.Contains(s, "发票（9%税率）") {
		return nil, nil, errors.New("企业通知收购地点、扣量或票据条件变化")
	}
	obs := []domain.Observation{}
	facts := []string{}
	for _, kind := range []string{"农户", "带票"} {
		pattern := regexp.MustCompile(`门前` + kind + `价格:新粮14%<水分≤15%三等及以上玉米折标价格为([0-9]+(?:\.[0-9]+)?)元/吨[；;]15%<水分≤38%玉米折标价格为([0-9]+(?:\.[0-9]+)?)元/吨`)
		prices := pattern.FindAllStringSubmatch(s, -1)
		if len(prices) != 1 {
			return nil, nil, errors.New("企业通知价格、水分档、等级或单位变化")
		}
		for i, moisture := range []string{"14%<水分≤15% · 三等及以上", "15%<水分≤38%"} {
			v, e := value(prices[0][i+1], 100, 10000)
			if e != nil {
				return nil, nil, e
			}
			entity := "吉林燃料乙醇 · 门前" + kind + " · " + moisture + " · 新粮折标价"
			scope := note + "门前" + kind + "报价。"
			if kind == "带票" {
				scope += "需9%税率发票；发票入账后10个工作日内付款。"
			} else {
				scope += "结算后1个工作日内付款。"
			}
			facts = append(facts, fmt.Sprintf("%s %.0f元/吨", entity, v))
			if !effective.After(now) {
				obs = append(obs, observation(address, source, "purchase_price", "吉林·吉林经济技术开发区", entity, v, "元/吨", effective.UTC(), now, scope))
			}
		}
	}
	// The announcement is immutable across its effective date; only observation
	// eligibility changes. Re-reading it tomorrow must not create another event.
	e := event(address, "企业收购公告｜"+title, source, "purchase_price", "吉林", "按公告生效时刻处理；生效前仅保留公告事件，不写入当前报价观测。"+strings.Join(facts, "；")+"。"+note, published, now)
	return obs, []domain.Event{e}, nil
}
