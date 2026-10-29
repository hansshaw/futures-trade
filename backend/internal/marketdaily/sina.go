// Package marketdaily fetches public, actual-contract daily observations for
// research. It never writes the backtest bar store or joins continuous symbols.
package marketdaily

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
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

const Source = "新浪公开实际合约日线"
const endpoint = "https://stock2.finance.sina.com.cn/futures/api/jsonp.php/var%20_cornwatch=/InnerFuturesNewService.getDailyKLine?symbol="
const maxBodyBytes = 4 * 1024 * 1024
const prefix = "/*<script>location.href='//sina.com';</script>*/"

var china = time.FixedZone("Asia/Shanghai", 8*3600)
var symbolRE = regexp.MustCompile(`^(c|cs|lh|m)([0-9]{2})([0-9]{2})$`)
var wrapperRE = regexp.MustCompile(`(?s)^var _cornwatch=\((\[.*\])\);?$`)

// HasOpenInterest/HasSettlement are true only when every included mature bar
// has that field. Zero placeholders for absent optional fields are not data.
type Report struct {
	Bars                     []domain.Bar `json:"bars"`
	Source                   string       `json:"source"`
	SourceURL                string       `json:"source_url"`
	AvailableAt              time.Time    `json:"available_at"`
	LastTradingDate          string       `json:"last_trading_date"`
	HasOpenInterest          bool         `json:"has_open_interest"`
	HasSettlement            bool         `json:"has_settlement"`
	MissingOpenInterestDates []string     `json:"missing_open_interest_dates,omitempty"`
	MissingSettlementDates   []string     `json:"missing_settlement_dates,omitempty"`
	ExcludedIncomplete       []string     `json:"excluded_incomplete"`
	SHA256                   string       `json:"sha256"`
	Warnings                 []string     `json:"warnings"`
}

type Collector struct{ Client *http.Client }

func New() *Collector {
	return &Collector{Client: &http.Client{Timeout: 18 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
		return errors.New("公开日线源不允许重定向")
	}}}
}

func validSymbol(symbol string) bool {
	m := symbolRE.FindStringSubmatch(symbol)
	if m == nil {
		return false
	}
	month, _ := strconv.Atoi(m[3])
	return month >= 1 && month <= 12 && (month%2 == 1 || (m[1] == "m" && (month == 8 || month == 12)))
}

// Fetch uses now as the conservative completion boundary, normally time.Now().
// The source is a mutable public observation, not an exchange finality promise.
func (c *Collector) Fetch(ctx context.Context, symbol string, now time.Time) (Report, error) {
	symbol = strings.ToLower(strings.TrimSpace(symbol))
	if !validSymbol(symbol) {
		return Report{}, errors.New("只支持 c/cs/lh/m 的实际交割月合约")
	}
	if c.Client == nil {
		return Report{}, errors.New("公开日线HTTP客户端未配置")
	}
	if now.IsZero() {
		return Report{}, errors.New("必须提供日线成熟度核验时刻")
	}
	url := endpoint + strings.ToUpper(symbol)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Report{}, err
	}
	req.Header.Set("Referer", "https://finance.sina.com.cn/")
	req.Header.Set("User-Agent", "CornWatch/0.4 public daily research")
	response, err := c.Client.Do(req)
	if err != nil {
		return Report{}, errors.New("公开日线源网络错误或超时")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return Report{}, fmt.Errorf("公开日线源返回HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxBodyBytes+1))
	if err != nil || len(body) > maxBodyBytes {
		return Report{}, errors.New("公开日线响应过大或读取失败")
	}
	report, err := Parse(body, symbol, now)
	if err != nil {
		return Report{}, err
	}
	report.AvailableAt = time.Now().UTC()
	return report, nil
}

type sourceBar struct {
	Date       string `json:"d"`
	Open       string `json:"o"`
	High       string `json:"h"`
	Low        string `json:"l"`
	Close      string `json:"c"`
	Volume     string `json:"v"`
	Interest   string `json:"p"`
	Settlement string `json:"s"`
}

// Parse unwraps only the observed fixed JSONP grammar; no JavaScript runs.
// Dates are provider trading dates. A date equal to the Shanghai calendar day
// is withheld until 15:15; later dates (including the current night session's
// next trading day) are withheld. It does not infer holidays from weekdays.
func Parse(body []byte, symbol string, now time.Time) (Report, error) {
	symbol = strings.ToLower(strings.TrimSpace(symbol))
	if !validSymbol(symbol) || now.IsZero() {
		return Report{}, errors.New("合约或核验时刻无效")
	}
	if len(body) == 0 || len(body) > maxBodyBytes || !utf8.Valid(body) {
		return Report{}, errors.New("公开日线响应为空、过大或编码无效")
	}
	text := strings.TrimSpace(string(body))
	text = strings.TrimSpace(strings.TrimPrefix(text, prefix))
	m := wrapperRE.FindStringSubmatch(text)
	if m == nil {
		return Report{}, errors.New("公开日线JSONP结构变化，停止解析")
	}
	var rows []sourceBar
	decoder := json.NewDecoder(bytes.NewBufferString(m[1]))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&rows); err != nil {
		return Report{}, errors.New("公开日线JSON或字段结构无效")
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF {
		return Report{}, errors.New("公开日线JSON包含额外内容")
	}
	if len(rows) == 0 || len(rows) > 6000 {
		return Report{}, errors.New("公开日线记录数无效")
	}
	hash := sha256.Sum256(body)
	report := Report{Bars: []domain.Bar{}, Source: Source, SourceURL: endpoint + strings.ToUpper(symbol), AvailableAt: now.UTC(), HasOpenInterest: true, HasSettlement: true, ExcludedIncomplete: []string{}, SHA256: hex.EncodeToString(hash[:]), Warnings: []string{
		"公开网页研究源没有交易所实时或最终结算保证；历史数值可能更正。",
		"仅纳入源交易日期早于上海今天，或今天15:15之后的日线；夜盘快照另列，不并入成熟日线。",
		"成交量和持仓量沿用源口径；总持仓变化不能识别参与者净多空或主力资金方向。",
	}}
	local := now.In(china)
	today := local.Format("2006-01-02")
	closedToday := local.Hour() > 15 || (local.Hour() == 15 && local.Minute() >= 15)
	previous := ""
	for _, row := range rows {
		day, err := time.ParseInLocation("2006-01-02", row.Date, china)
		if err != nil || day.Format("2006-01-02") != row.Date || (previous != "" && row.Date <= previous) {
			return Report{}, errors.New("公开日线日期无效、重复或乱序")
		}
		previous = row.Date
		bar := domain.Bar{Symbol: symbol, TradeDate: row.Date, Timestamp: day.Add(15 * time.Hour), IsDemo: false}
		for _, pair := range []struct {
			text string
			out  *float64
		}{{row.Open, &bar.Open}, {row.High, &bar.High}, {row.Low, &bar.Low}, {row.Close, &bar.Close}} {
			n, err := number(pair.text)
			if err != nil || n <= 0 {
				return Report{}, fmt.Errorf("%s公开日线价格无效", row.Date)
			}
			*pair.out = n
		}
		if bar.High < math.Max(bar.Open, bar.Close) || bar.Low > math.Min(bar.Open, bar.Close) || bar.High < bar.Low {
			return Report{}, fmt.Errorf("%s公开日线OHLC不一致", row.Date)
		}
		bar.Volume, err = integer(row.Volume)
		if err != nil {
			return Report{}, fmt.Errorf("%s公开日线成交量无效", row.Date)
		}
		if row.Interest != "" {
			bar.OpenInterest, err = integer(row.Interest)
			if err != nil {
				return Report{}, fmt.Errorf("%s公开日线持仓量无效", row.Date)
			}
		}
		if row.Settlement != "" {
			bar.Settlement, err = number(row.Settlement)
			if err != nil || bar.Settlement < 0 {
				return Report{}, fmt.Errorf("%s公开日线结算价无效", row.Date)
			}
		}
		if row.Date > today || (row.Date == today && !closedToday) {
			report.ExcludedIncomplete = append(report.ExcludedIncomplete, row.Date)
			continue
		}
		if row.Interest == "" {
			report.HasOpenInterest = false
			report.MissingOpenInterestDates = append(report.MissingOpenInterestDates, row.Date)
		}
		if bar.Settlement == 0 {
			report.HasSettlement = false
			report.MissingSettlementDates = append(report.MissingSettlementDates, row.Date)
		}
		report.Bars = append(report.Bars, bar)
	}
	if len(report.Bars) == 0 {
		return Report{}, errors.New("尚无通过收盘时刻校验的成熟日线")
	}
	report.LastTradingDate = report.Bars[len(report.Bars)-1].TradeDate
	if !report.HasOpenInterest {
		report.Warnings = append(report.Warnings, "部分成熟日线缺少持仓字段，缺失日期的零值仅为占位，不能计算为实际持仓。")
	}
	if !report.HasSettlement {
		report.Warnings = append(report.Warnings, "部分成熟日线缺少结算价，不用收盘价补齐，不能直接用于逐日盯市回测。")
	}
	if len(report.ExcludedIncomplete) > 0 {
		report.Warnings = append(report.Warnings, fmt.Sprintf("已排除%d条尚未达到收盘确认时刻的记录。", len(report.ExcludedIncomplete)))
	}
	return report, nil
}

func number(text string) (float64, error) {
	n, err := strconv.ParseFloat(text, 64)
	if err != nil || math.IsNaN(n) || math.IsInf(n, 0) || math.Abs(n) > 1e12 {
		return 0, errors.New("invalid number")
	}
	return n, nil
}
func integer(text string) (int64, error) {
	n, err := number(text)
	if err != nil || n < 0 || n != math.Trunc(n) {
		return 0, errors.New("invalid integer")
	}
	return int64(n), nil
}
