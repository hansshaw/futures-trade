// Package importcorn reads the public Sinograin imported-corn auction results.
// An auction of imported inventory is not a cross-border import transaction.
package importcorn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"cornwatch/internal/domain"
	"cornwatch/internal/store"
	"golang.org/x/net/html"
)

const SourceName = "中储粮网 · 进口玉米竞价销售结果"
const ListURL = "https://www.esinograin.com/api/others/getTransactionAnnouncementListFromIndex?rows=10&page=1"
const DetailURL = "https://www.esinograin.com/api/others/getTransactionAnnouncementDetail"
const PublicListURL = "https://www.esinograin.com/resources/static/newindex/notice/gonggaolist.html?type=lyjyjg"
const responseLimit = 1024 * 1024

var china = time.FixedZone("Asia/Shanghai", 8*3600)
var identifier = regexp.MustCompile(`^[a-f0-9]{32}$`)
var titlePattern = regexp.MustCompile(`^中储粮网(?:(\d{4})年)?(\d{1,2})月(\d{1,2})日进口玉米(?:（转基因）|\(转基因\))?竞价销售结果$`)
var ratePattern = regexp.MustCompile(`^(\d{1,3}(?:\.\d{1,4})?)[%％]$`)

type Collector struct {
	Client       *http.Client
	Now          func() time.Time
	mu           sync.Mutex
	cachedAt     time.Time
	observations []domain.Observation
	events       []domain.Event
}

func New() *Collector {
	return &Collector{Client: &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
		return errors.New("进口玉米公开来源不跟随重定向")
	}}, Now: time.Now}
}

// Auction checks the latest matching public result, not every calendar date.
// It makes at most two requests and briefly reuses successful results so that a
// connection test immediately followed by sync does not hammer the source.
// Cached observations retain their actual original AvailableAt.
func (c *Collector) Auction(ctx context.Context) ([]domain.Observation, []domain.Event, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now
	if c.Now != nil {
		now = c.Now
	}
	started := now().UTC()
	if !c.cachedAt.IsZero() && started.Sub(c.cachedAt) >= 0 && started.Sub(c.cachedAt) < time.Minute {
		o, e := clone(c.observations, c.events)
		return o, e, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 35*time.Second)
	defer cancel()
	body, err := c.post(ctx, ListURL, url.Values{"jyggLx": {"交易结果"}, "jyggBt": {"进口玉米"}})
	if err != nil {
		return nil, nil, err
	}
	item, err := parseList(body, started)
	if err != nil {
		return nil, nil, err
	}
	if item == nil {
		c.cachedAt, c.observations, c.events = started, nil, nil
		return []domain.Observation{}, []domain.Event{}, nil
	}
	body, err = c.post(ctx, DetailURL, url.Values{"jyggId": {item.ID}, "aucId": {""}})
	if err != nil {
		return nil, nil, err
	}
	observations, events, err := parseDetail(body, *item, now().UTC())
	if err != nil {
		return nil, nil, err
	}
	c.cachedAt, c.observations, c.events = now().UTC(), observations, events
	o, e := clone(observations, events)
	return o, e, nil
}

func clone(observations []domain.Observation, events []domain.Event) ([]domain.Observation, []domain.Event) {
	o := append([]domain.Observation{}, observations...)
	e := append([]domain.Event{}, events...)
	for i := range e {
		e[i].Products = append([]string{}, e[i].Products...)
	}
	return o, e
}

func (c *Collector) post(ctx context.Context, address string, values url.Values) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, address, strings.NewReader(values.Encode()))
	if err != nil {
		return nil, errors.New("进口玉米来源请求构造失败")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Referer", "https://www.esinograin.com/")
	req.Header.Set("User-Agent", "CornWatch/0.4 public auction research")
	client := c.Client
	if client == nil {
		client = New().Client
	}
	response, err := client.Do(req)
	if err != nil {
		return nil, errors.New("中储粮公开结果网络错误或超时；保留上次数据")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("中储粮公开结果 HTTP %d；保留上次数据", response.StatusCode)
	}
	if response.Request != nil && response.Request.URL != nil && (response.Request.URL.Scheme != "https" || response.Request.URL.Host != "www.esinograin.com") {
		return nil, errors.New("中储粮公开结果来源发生变化")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, responseLimit+1))
	if err != nil || len(body) > responseLimit {
		return nil, errors.New("中储粮公开结果响应过大或读取失败")
	}
	if strings.Contains(string(body), "当前访问人数较多") {
		return nil, errors.New("中储粮公开结果暂时限流，请稍后再试；保留上次数据")
	}
	return body, nil
}

type announcement struct {
	ID        string `json:"JYGG_ID"`
	Title     string `json:"JYGG_BT"`
	Kind      string `json:"JYGG_LX"`
	Date      string `json:"JYGG_SJ"`
	OrderTime string `json:"ORDERSJ"`
}

func parseList(body []byte, now time.Time) (*announcement, error) {
	var payload struct {
		Status *int `json:"status"`
		Data   *struct {
			Rows []announcement `json:"rows"`
		} `json:"data"`
	}
	if len(body) > responseLimit || json.Unmarshal(body, &payload) != nil || payload.Status == nil || *payload.Status != 0 || payload.Data == nil || len(payload.Data.Rows) > 100 {
		return nil, errors.New("中储粮公开结果列表格式或业务状态无效")
	}
	rows := []announcement{}
	seen := map[string]bool{}
	for _, item := range payload.Data.Rows {
		if item.Kind != "交易结果" || !titlePattern.MatchString(item.Title) {
			continue
		}
		if !identifier.MatchString(item.ID) || seen[item.ID] {
			return nil, errors.New("中储粮进口玉米结果标识无效或重复")
		}
		date, err := time.ParseInLocation("2006-01-02", item.Date, china)
		if err != nil || date.After(now) {
			return nil, errors.New("中储粮进口玉米结果发布日期无效或来自未来")
		}
		if _, err := auctionDate(item.Title, date); err != nil {
			return nil, err
		}
		if item.OrderTime != "" {
			order, err := time.ParseInLocation("2006-01-02 15:04:05", item.OrderTime, china)
			if err != nil || order.After(now) || order.Format("2006-01-02") != item.Date {
				return nil, errors.New("中储粮结果列表发布时间不一致")
			}
		}
		seen[item.ID] = true
		rows = append(rows, item)
	}
	if len(rows) == 0 {
		return nil, nil
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Date != rows[j].Date {
			return rows[i].Date > rows[j].Date
		}
		if rows[i].OrderTime != rows[j].OrderTime {
			return rows[i].OrderTime > rows[j].OrderTime
		}
		return rows[i].ID < rows[j].ID
	})
	return &rows[0], nil
}

func auctionDate(title string, published time.Time) (time.Time, error) {
	match := titlePattern.FindStringSubmatch(title)
	if match == nil {
		return time.Time{}, errors.New("不是可识别的进口玉米竞价销售结果")
	}
	year := published.In(china).Year()
	if match[1] != "" {
		year, _ = strconv.Atoi(match[1])
	}
	month, _ := strconv.Atoi(match[2])
	day, _ := strconv.Atoi(match[3])
	date := time.Date(year, time.Month(month), day, 0, 0, 0, 0, china)
	if int(date.Month()) != month || date.Day() != day || date.Year() != year || date.After(published) {
		return time.Time{}, errors.New("进口玉米场次日期无效或晚于结果发布时间")
	}
	return date, nil
}

func parseDetail(body []byte, item announcement, received time.Time) ([]domain.Observation, []domain.Event, error) {
	var payload struct {
		Status *int `json:"status"`
		Data   *struct {
			ID        string `json:"jyggId"`
			Title     string `json:"jyggBt"`
			Kind      string `json:"jyggLx"`
			Published string `json:"jyggSj"`
			HTML      string `json:"jyggNr"`
		} `json:"data"`
	}
	if len(body) > responseLimit || json.Unmarshal(body, &payload) != nil || payload.Status == nil || *payload.Status != 0 || payload.Data == nil {
		return nil, nil, errors.New("中储粮进口玉米详情格式或业务状态无效")
	}
	d := payload.Data
	if strings.Contains(d.HTML, "当前访问人数较多") {
		return nil, nil, errors.New("中储粮公开结果暂时限流；保留上次数据")
	}
	if d.ID != item.ID || d.Title != item.Title || d.Kind != "交易结果" || d.HTML == "" {
		return nil, nil, errors.New("中储粮结果详情与列表不一致或正文不可用")
	}
	published, err := time.ParseInLocation("2006-01-02 15:04:05", d.Published, china)
	if err != nil || published.After(received) || published.Format("2006-01-02") != item.Date {
		return nil, nil, errors.New("中储粮结果发布时间无效")
	}
	date, err := auctionDate(d.Title, published)
	if err != nil {
		return nil, nil, err
	}
	rate, err := totalRate(d.HTML)
	if err != nil {
		return nil, nil, err
	}
	sourceURL := "https://www.esinograin.com/resources/getGgInfo?ggid=" + item.ID + "&ubd=0215"
	value := strconv.FormatFloat(rate, 'f', -1, 64)
	note := "原始公告合计成交率，保留来源发布值，不由其他渠道吨数重算。按场次发布，非每日必有交易；这是已进口玉米库存在国内竞价销售，不是当天新签进口、报关进口量或到港量。公开汇总未披露投放量、成交吨数及成交价，不据百分比反推。observed_at仅表示场次日期，以北京时间零点存储；公告发布时间为" + published.Format("2006-01-02 15:04:05") + "（北京时间）。公告ID：" + item.ID + "。"
	version := store.ID(item.ID, d.Title, d.Published, value)
	o := domain.Observation{ID: store.ID("sinograin-import-auction", version, "rate"), Kind: "import_auction_rate", Region: "中国·进口玉米库存国内销售", Entity: "中储粮进口玉米拍卖·合计成交率", Value: rate, Unit: "%", ObservedAt: date, AvailableAt: received, Source: SourceName, SourceURL: sourceURL, Note: note}
	e := domain.Event{ID: store.ID("sinograin-import-auction", version, "event"), Title: d.Title, Body: date.Format("2006-01-02") + "场次：原始公告合计成交率 " + value + "%。" + note, Category: "import_auction", Region: o.Region, Products: []string{"c"}, Source: SourceName, SourceURL: sourceURL, PublishedAt: published, IngestedAt: received, AvailableAt: received, Severity: "medium"}
	return []domain.Observation{o}, []domain.Event{e}, nil
}

func nodeText(n *html.Node) string {
	if n.Type == html.ElementNode && (n.Data == "script" || n.Data == "style") {
		return ""
	}
	var out strings.Builder
	if n.Type == html.TextNode {
		out.WriteString(n.Data)
	}
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		out.WriteString(nodeText(child))
	}
	return out.String()
}

func totalRate(markup string) (float64, error) {
	doc, err := html.Parse(strings.NewReader(markup))
	if err != nil {
		return 0, errors.New("中储粮结果表格解析失败")
	}
	var rates []float64
	var malformed bool
	var visit func(*html.Node)
	visit = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "table" {
			var rows [][]string
			var collect func(*html.Node)
			collect = func(p *html.Node) {
				if p != n && p.Type == html.ElementNode && p.Data == "table" {
					return
				}
				if p.Type == html.ElementNode && p.Data == "tr" {
					var cells []string
					for cell := p.FirstChild; cell != nil; cell = cell.NextSibling {
						if cell.Type == html.ElementNode && (cell.Data == "td" || cell.Data == "th") {
							cells = append(cells, strings.Join(strings.Fields(nodeText(cell)), ""))
						}
					}
					rows = append(rows, cells)
				}
				for child := p.FirstChild; child != nil; child = child.NextSibling {
					collect(child)
				}
			}
			collect(n)
			if len(rows) < 2 {
				return
			}
			// Accept only the verified public summary schema. A changed schema is
			// reviewed before extraction, never silently treated as the same series.
			if len(rows[0]) != 2 || rows[0][0] != "销售单位" || rows[0][1] != "成交率" {
				return
			}
			for _, row := range rows[1:] {
				if len(row) == 0 || row[0] != "合计" {
					continue
				}
				if len(row) != 2 {
					malformed = true
					continue
				}
				match := ratePattern.FindStringSubmatch(row[1])
				if match == nil {
					malformed = true
					continue
				}
				value, err := strconv.ParseFloat(match[1], 64)
				if err != nil || math.IsNaN(value) || value < 0 || value > 100 {
					malformed = true
					continue
				}
				rates = append(rates, value)
			}
			return
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			visit(child)
		}
	}
	visit(doc)
	if malformed || len(rates) != 1 {
		return 0, errors.New("进口玉米结果缺少唯一有效的合计成交率；不补零或推算吨数")
	}
	return rates[0], nil
}
