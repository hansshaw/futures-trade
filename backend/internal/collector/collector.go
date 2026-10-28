package collector

import (
	"context"
	"cornwatch/internal/domain"
	"cornwatch/internal/store"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type Collector struct{ Client *http.Client }

func New() *Collector {
	return &Collector{Client: &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) > 3 {
			return errors.New("too many redirects")
		}
		if req.URL.Scheme != "https" {
			return errors.New("HTTPS required")
		}
		return nil
	}}}
}
func (c *Collector) get(ctx context.Context, address string) ([]byte, error) {
	u, err := url.Parse(address)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		return nil, errors.New("采集地址必须为 HTTPS URL")
	}
	req, err := http.NewRequestWithContext(ctx, "GET", address, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "CornWatch/0.1 research collector")
	resp, err := c.Client.Do(req)
	if err != nil {
		return nil, errors.New("采集网络错误或超时")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("数据源返回 HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024+1))
	if err != nil || len(data) > 2*1024*1024 {
		return nil, errors.New("数据源响应过大或读取失败")
	}
	return data, nil
}

// Weather records the retrieval time as available_at, not the forecast target
// time. It is a forward forecast snapshot, never a historical observation.
func (c *Collector) Weather(ctx context.Context) ([]domain.Observation, error) {
	locations := []struct {
		name     string
		lat, lon float64
	}{{"黑龙江·哈尔滨", 45.75, 126.63}, {"吉林·长春", 43.88, 125.32}, {"辽宁·沈阳", 41.80, 123.43}, {"山东·潍坊", 36.71, 119.16}, {"河南·郑州", 34.75, 113.62}}
	result := []domain.Observation{}
	for _, loc := range locations {
		address := fmt.Sprintf("https://api.open-meteo.com/v1/forecast?latitude=%.2f&longitude=%.2f&daily=precipitation_sum,temperature_2m_max,temperature_2m_min&timezone=Asia%%2FShanghai&forecast_days=3", loc.lat, loc.lon)
		data, err := c.get(ctx, address)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", loc.name, err)
		}
		var response struct {
			Daily struct {
				Time []string   `json:"time"`
				Rain []*float64 `json:"precipitation_sum"`
				Max  []*float64 `json:"temperature_2m_max"`
				Min  []*float64 `json:"temperature_2m_min"`
			} `json:"daily"`
		}
		if json.Unmarshal(data, &response) != nil || len(response.Daily.Time) == 0 || len(response.Daily.Rain) != len(response.Daily.Time) || len(response.Daily.Max) != len(response.Daily.Time) || len(response.Daily.Min) != len(response.Daily.Time) {
			return nil, errors.New("天气数据结构不完整")
		}
		now := time.Now().UTC()
		for i, date := range response.Daily.Time {
			forecastDate, err := time.ParseInLocation("2006-01-02", date, time.FixedZone("Asia/Shanghai", 8*3600))
			if err != nil {
				return nil, errors.New("天气日期格式无效")
			}
			for _, metric := range []struct {
				name, unit string
				value      *float64
			}{{"日降水预报", "mm", response.Daily.Rain[i]}, {"最高温预报", "°C", response.Daily.Max[i]}, {"最低温预报", "°C", response.Daily.Min[i]}} {
				if metric.value == nil {
					continue
				}
				value := *metric.value
				result = append(result, domain.Observation{ID: store.ID("open-meteo", loc.name, date, metric.name, now.Format("2006-01-02T15"), strconv.FormatFloat(value, 'f', 2, 64)), Kind: "weather", Region: loc.name, Entity: metric.name + " · " + date, Value: value, Unit: metric.unit, ObservedAt: forecastDate, AvailableAt: now, Source: "Open-Meteo", SourceURL: address, Note: "前瞻天气预报快照；observed_at为预报目标日期，available_at为采集时间；点位不代表全省。Weather data by Open-Meteo.com (CC BY 4.0)，仅转换为观测结构"})
			}
		}
	}
	return result, nil
}

type rss struct {
	Channel struct {
		Title string `xml:"title"`
		Items []struct {
			Title       string `xml:"title"`
			Description string `xml:"description"`
			Link        string `xml:"link"`
			PubDate     string `xml:"pubDate"`
		} `xml:"item"`
	} `xml:"channel"`
}
type atom struct {
	Title   string `xml:"title"`
	Entries []struct {
		Title     string `xml:"title"`
		Summary   string `xml:"summary"`
		Content   string `xml:"content"`
		Published string `xml:"published"`
		Updated   string `xml:"updated"`
		Links     []struct {
			Href string `xml:"href,attr"`
			Rel  string `xml:"rel,attr"`
		} `xml:"link"`
	} `xml:"entry"`
}

var tags = regexp.MustCompile(`<[^>]*>`)

func clean(s string) string {
	return strings.TrimSpace(html.UnescapeString(tags.ReplaceAllString(s, " ")))
}
func parseTime(s string) (time.Time, error) {
	for _, layout := range []string{time.RFC3339Nano, time.RFC1123Z, time.RFC1123, time.RFC822Z, time.RFC822} {
		if t, e := time.Parse(layout, s); e == nil {
			return t, nil
		}
	}
	return time.Time{}, errors.New("feed item needs a valid publication timestamp")
}

func ParseFeed(data []byte, feedURL string, now time.Time) ([]domain.Event, error) {
	var r rss
	var a atom
	if xml.Unmarshal(data, &r) != nil {
		return nil, errors.New("RSS/Atom XML 格式错误")
	}
	items := []domain.Event{}
	add := func(title, body, link, date, source string) {
		published, err := parseTime(date)
		if err != nil || published.After(now.Add(5*time.Minute)) {
			return
		}
		title = clean(title)
		body = clean(body)
		if title == "" || len(title) > 1000 || len(body) > 64000 {
			return
		}
		if source == "" {
			source = "授权 Feed"
		}
		linkURL, e := url.Parse(strings.TrimSpace(link))
		if e != nil || linkURL.Scheme != "https" && linkURL.Scheme != "http" {
			link = feedURL
		}
		category := "news"
		region := "待核实"
		for _, p := range []string{"黑龙江", "吉林", "辽宁", "山东", "河南", "河北"} {
			if strings.Contains(title+body, p) {
				region = p
				break
			}
		}
		for _, pair := range [][2]string{{"到车", "truck_arrivals"}, {"收购", "purchase_price"}, {"降雨", "weather"}, {"天气", "weather"}, {"淀粉", "related_price"}, {"生猪", "related_price"}} {
			if strings.Contains(title+body, pair[0]) {
				category = pair[1]
				break
			}
		}
		available := now
		if published.After(available) {
			available = published
		}
		items = append(items, domain.Event{ID: store.ID(source, link, title, body, published.UTC().Format(time.RFC3339Nano)), Title: title, Body: body, Category: category, Region: region, Products: []string{"c"}, Source: source, SourceURL: link, PublishedAt: published, IngestedAt: now, AvailableAt: available, Severity: "medium"})
	}
	for _, item := range r.Channel.Items {
		add(item.Title, item.Description, item.Link, item.PubDate, r.Channel.Title)
	}
	if len(r.Channel.Items) == 0 {
		if xml.Unmarshal(data, &a) != nil {
			return nil, errors.New("Atom XML 格式错误")
		}
		for _, item := range a.Entries {
			date := item.Published
			if date == "" {
				date = item.Updated
			}
			body := item.Summary
			if body == "" {
				body = item.Content
			}
			link := ""
			for _, l := range item.Links {
				if l.Rel == "alternate" || l.Rel == "" {
					link = l.Href
					break
				}
			}
			add(item.Title, body, link, date, a.Title)
		}
	}
	if len(items) == 0 {
		return nil, errors.New("Feed 没有带有效发布时间的条目")
	}
	return items, nil
}
func (c *Collector) Feed(ctx context.Context, address string) ([]domain.Event, error) {
	data, err := c.get(ctx, address)
	if err != nil {
		return nil, err
	}
	return ParseFeed(data, address, time.Now().UTC())
}
