package marketchart

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"cornwatch/internal/domain"
)

const minuteEndpoint = "https://stock2.finance.sina.com.cn/futures/api/jsonp.php/var%20_cornwatch=/InnerFuturesNewService.getFewMinLine"
const maxBody = 4 * 1024 * 1024

var wrapperRE = regexp.MustCompile(`(?s)^var _cornwatch=\((\[.*\])\);?$`)

type minuteCollector struct{ client *http.Client }

func newMinuteCollector() *minuteCollector {
	return &minuteCollector{client: &http.Client{Timeout: 18 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("公开分钟线不允许重定向") }}}
}
func (c *minuteCollector) fetch(ctx context.Context, symbol, interval string, at time.Time) (Report, error) {
	if !validSymbol(symbol) || minutes(interval) == 0 || at.IsZero() {
		return Report{}, errors.New("分钟线合约、周期或时点无效")
	}
	url := minuteURL(symbol, interval)
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if e != nil {
		return Report{}, e
	}
	req.Header.Set("Referer", "https://finance.sina.com.cn/")
	req.Header.Set("User-Agent", "CornWatch/0.6 public chart research")
	resp, e := c.client.Do(req)
	if e != nil {
		return Report{}, errors.New("公开分钟线网络错误或超时")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Report{}, fmt.Errorf("公开分钟线返回HTTP %d", resp.StatusCode)
	}
	b, e := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if e != nil || len(b) > maxBody {
		return Report{}, errors.New("分钟线响应过大或读取失败")
	}
	report, e := parseMinute(b, symbol, interval, at)
	if e != nil {
		return Report{}, e
	}
	report.AvailableAt = time.Now().UTC()
	for i := range report.Bars {
		report.Bars[i].AvailableAt = report.AvailableAt
	}
	return report, nil
}
func minuteURL(symbol, interval string) string {
	return minuteEndpoint + "?symbol=" + strings.ToUpper(symbol) + "&type=" + strconv.Itoa(minutes(interval))
}

type row struct {
	Date     string `json:"d"`
	Open     string `json:"o"`
	High     string `json:"h"`
	Low      string `json:"l"`
	Close    string `json:"c"`
	Volume   string `json:"v"`
	Interest string `json:"p"`
}

func parseMinute(body []byte, symbol, interval string, at time.Time) (Report, error) {
	r := Report{Symbol: symbol, Interval: interval, Source: "新浪公开实际合约分钟线", SourceURL: minuteURL(symbol, interval), AvailableAt: at.UTC(), Bars: []Bar{}, HasOpenInterest: true, Warnings: []string{
		"公开历史窗口由来源决定，不保证完整历史、实时性或最终结算；未拼接不同合约，也未自动写入回测库。",
		"分钟时间沿用来源的北京时间自然时刻，不据此推断夜盘交易日。为避免未完成K线，保守地在源时间再经过一个完整周期后纳入；可能比来源的收盘标记额外滞后一个周期。",
		"成交量和持仓量仅保留来源字段；未核验跨周期成交量可加总性，不能将其用于精确撮合、逐笔重建或资金流向推断。",
	}}
	if !validSymbol(symbol) || minutes(interval) == 0 || at.IsZero() || len(body) == 0 || len(body) > maxBody || !utf8.Valid(body) {
		return r, errors.New("分钟线参数、长度或编码无效")
	}
	text := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(body)), "/*<script>location.href='//sina.com';</script>*/"))
	matches := wrapperRE.FindStringSubmatch(text)
	if matches == nil {
		return r, errors.New("分钟线JSONP结构变化，停止解析")
	}
	if uniqueJSON([]byte(matches[1])) != nil {
		return r, errors.New("分钟线JSON无效或含重复字段")
	}
	dec := json.NewDecoder(bytes.NewBufferString(matches[1]))
	dec.DisallowUnknownFields()
	var rows []row
	if e := dec.Decode(&rows); e != nil {
		return r, errors.New("分钟线JSON或字段结构无效")
	}
	var tail any
	if dec.Decode(&tail) != io.EOF {
		return r, errors.New("分钟线包含额外JSON")
	}
	if len(rows) == 0 || len(rows) > MaxBars {
		return r, errors.New("分钟线记录数无效")
	}
	var previous time.Time
	excluded := 0
	for _, v := range rows {
		stamp, e := time.ParseInLocation("2006-01-02 15:04:05", v.Date, china)
		if e != nil || stamp.Format("2006-01-02 15:04:05") != v.Date || (!previous.IsZero() && !stamp.After(previous)) {
			return r, errors.New("分钟线时间无效、重复或乱序")
		}
		previous = stamp
		b := domain.Bar{Symbol: symbol, Timestamp: stamp}
		for _, p := range []struct {
			s   string
			out *float64
		}{{v.Open, &b.Open}, {v.High, &b.High}, {v.Low, &b.Low}, {v.Close, &b.Close}} {
			n, e := number(p.s)
			if e != nil {
				return r, e
			}
			*p.out = n
		}
		b.Volume, e = integer(v.Volume)
		if e != nil {
			return r, e
		}
		if v.Interest != "" {
			b.OpenInterest, e = integer(v.Interest)
			if e != nil {
				return r, e
			}
		}
		if !validBar(b, symbol) {
			return r, errors.New("分钟线OHLC、成交量或持仓无效")
		}
		if !mature(b, interval, at) {
			excluded++
			continue
		}
		if v.Interest == "" {
			r.HasOpenInterest = false
		}
		r.Bars = append(r.Bars, Bar{Bar: b, AvailableAt: at.UTC()})
	}
	if len(r.Bars) == 0 {
		return r, errors.New("没有通过保守完成时刻校验的分钟线")
	}
	if excluded > 0 {
		r.Warnings = append(r.Warnings, fmt.Sprintf("已排除%d条尚未达到保守完成时刻的分钟记录。", excluded))
	}
	if !r.HasOpenInterest {
		r.Warnings = append(r.Warnings, "部分分钟持仓字段缺失；零值占位不能解释为真实持仓。")
	}
	return r, nil
}
func number(s string) (float64, error) {
	v, e := strconv.ParseFloat(s, 64)
	if e != nil || math.IsNaN(v) || math.IsInf(v, 0) || math.Abs(v) > 1e12 {
		return 0, errors.New("分钟线数值无效")
	}
	return v, nil
}
func integer(s string) (int64, error) {
	v, e := number(s)
	if e != nil || v < 0 || v != math.Trunc(v) {
		return 0, errors.New("分钟线数量须为非负整数")
	}
	return int64(v), nil
}

// JSON's normal decoder silently overwrites duplicate object keys. Reject them
// at every depth so a displayed or validated report has one interpretation.
func uniqueJSON(raw []byte) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var walk func(int) error
	walk = func(depth int) error {
		if depth > 8 {
			return errors.New("JSON嵌套过深")
		}
		t, err := d.Token()
		if err != nil {
			return err
		}
		delim, ok := t.(json.Delim)
		if !ok {
			return nil
		}
		switch delim {
		case '{':
			seen := map[string]bool{}
			for d.More() {
				k, err := d.Token()
				if err != nil {
					return err
				}
				s, ok := k.(string)
				if !ok || seen[s] {
					return errors.New("重复JSON字段")
				}
				seen[s] = true
				if err = walk(depth + 1); err != nil {
					return err
				}
			}
		case '[':
			for d.More() {
				if err = walk(depth + 1); err != nil {
					return err
				}
			}
		default:
			return errors.New("无效JSON")
		}
		_, err = d.Token()
		return err
	}
	if err := walk(0); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return errors.New("多余JSON内容")
	}
	return nil
}
