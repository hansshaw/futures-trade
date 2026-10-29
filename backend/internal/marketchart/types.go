// Package marketchart serves provenance-preserving chart history separately
// from immutable backtest inputs. Public history is not an execution feed.
package marketchart

import (
	"context"
	"errors"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"cornwatch/internal/domain"
	"cornwatch/internal/marketdaily"
)

var china = time.FixedZone("Asia/Shanghai", 8*3600)
var symbolRE = regexp.MustCompile(`^(c|cs|lh|m)[0-9]{2}([0-9]{2})$`)

const MaxBars = 6000
const RefreshInterval = 60 * time.Second

type DailyFetcher func(context.Context, string, time.Time) (marketdaily.Report, error)
type Bar struct {
	domain.Bar
	AvailableAt time.Time `json:"available_at,omitzero"`
}
type Report struct {
	Symbol              string     `json:"symbol"`
	Interval            string     `json:"interval"`
	Bars                []Bar      `json:"bars"`
	Source              string     `json:"source"`
	SourceURL           string     `json:"source_url"`
	AvailableAt         time.Time  `json:"available_at,omitzero"`
	LastBarAt           *time.Time `json:"last_bar_at,omitempty"`
	Cached              bool       `json:"cached"`
	Stale               bool       `json:"stale"`
	LastError           string     `json:"last_error,omitempty"`
	Warnings            []string   `json:"warnings"`
	TotalAvailable      int        `json:"total_available"`
	RangeStart          string     `json:"range_start,omitempty"`
	RangeEnd            string     `json:"range_end,omitempty"`
	RefreshAfterSeconds int        `json:"refresh_after_seconds"`
	HasOpenInterest     bool       `json:"has_open_interest"`
	HasSettlement       bool       `json:"has_settlement"`
}
type Request struct {
	Symbol   string
	Interval string
	Source   string
	From     string
	To       string
	Limit    int
}

func (r Request) normalized() (Request, error) {
	r.Symbol = strings.ToLower(strings.TrimSpace(r.Symbol))
	if !validSymbol(r.Symbol) {
		return r, errors.New("仅支持 c/cs/lh/m 的实际交割月合约，不支持主连或指数拼接")
	}
	if r.Interval == "" {
		r.Interval = "day"
	}
	if r.Interval != "day" && minutes(r.Interval) == 0 {
		return r, errors.New("周期仅支持 day、1m、5m、15m、30m、60m")
	}
	if r.Source == "" {
		r.Source = "public"
	}
	if r.Source != "public" && r.Source != "imported" {
		return r, errors.New("历史来源仅支持 public 或 imported")
	}
	if r.Source == "imported" && r.Interval != "day" {
		return r, errors.New("本地导入当前只支持真实日线")
	}
	if r.Limit == 0 {
		r.Limit = 1000
	}
	if r.Limit < 1 || r.Limit > MaxBars {
		return r, errors.New("历史条数需在1至6000之间")
	}
	for _, d := range []string{r.From, r.To} {
		if d != "" {
			t, e := time.ParseInLocation("2006-01-02", d, china)
			if e != nil || t.Format("2006-01-02") != d {
				return r, errors.New("历史日期需为有效 YYYY-MM-DD")
			}
		}
	}
	if r.From != "" && r.To != "" && r.From > r.To {
		return r, errors.New("起始日期不能晚于结束日期")
	}
	return r, nil
}
func Validate(r Request) (Request, error) { return r.normalized() }
func validSymbol(symbol string) bool {
	m := symbolRE.FindStringSubmatch(symbol)
	if m == nil {
		return false
	}
	n, _ := strconv.Atoi(m[2])
	return n >= 1 && n <= 12 && (n%2 == 1 || (m[1] == "m" && (n == 8 || n == 12)))
}
func minutes(interval string) int {
	switch interval {
	case "1m":
		return 1
	case "5m":
		return 5
	case "15m":
		return 15
	case "30m":
		return 30
	case "60m":
		return 60
	}
	return 0
}
func validBar(b domain.Bar, symbol string) bool {
	if b.Symbol != symbol || b.IsDemo || b.Timestamp.IsZero() || b.Volume < 0 || b.OpenInterest < 0 {
		return false
	}
	for _, v := range []float64{b.Open, b.High, b.Low, b.Close} {
		if v <= 0 || v > 1e12 || math.IsNaN(v) || math.IsInf(v, 0) {
			return false
		}
	}
	return b.High >= math.Max(b.Open, b.Close) && b.Low <= math.Min(b.Open, b.Close) && b.High >= b.Low
}
func mature(b domain.Bar, interval string, at time.Time) bool {
	if interval != "day" {
		return !b.Timestamp.Add(time.Duration(minutes(interval)) * time.Minute).After(at)
	}
	d := at.In(china)
	day := d.Format("2006-01-02")
	return b.TradeDate != "" && (b.TradeDate < day || (b.TradeDate == day && (d.Hour()*60+d.Minute() >= 15*60+15))) && !b.Timestamp.After(at)
}
