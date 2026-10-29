package market

import (
	"context"
	"encoding/csv"
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
	"time"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
)

var china = time.FixedZone("Asia/Shanghai", 8*60*60)
var symbolPattern = regexp.MustCompile(`^(cs|lh|c|m)([0-9]{2})([0-9]{2})$`)
var assignmentPattern = regexp.MustCompile(`^var hq_str_nf_([A-Z]+[0-9]{4})="([^"\r\n]*)";$`)

const maxResponseBytes = 512 * 1024

type Collector struct {
	Client *http.Client
	Now    func() time.Time
}

func New() *Collector {
	return &Collector{Client: &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
		return errors.New("公开报价不跟随重定向")
	}}, Now: time.Now}
}

// ContractSymbols covers actual listed delivery-month patterns in the current
// month and the following 13 months. Empty responses for not-yet-listed symbols
// are expected. No continuous code is queried or treated as a tradable contract.
func ContractSymbols(now time.Time) []string {
	local := now.In(china)
	month := time.Date(local.Year(), local.Month(), 1, 0, 0, 0, 0, china)
	result := make([]string, 0, 40)
	for _, product := range []string{"c", "cs", "lh", "m"} {
		for i := 0; i < 14; i++ {
			date := month.AddDate(0, i, 0)
			m := int(date.Month())
			if m%2 == 0 && !(product == "m" && (m == 8 || m == 12)) {
				continue
			}
			result = append(result, fmt.Sprintf("%s%02d%02d", product, date.Year()%100, m))
		}
	}
	return result
}

func (c *Collector) Collect(ctx context.Context) ([]Quote, error) {
	now := time.Now
	if c.Now != nil {
		now = c.Now
	}
	symbols := ContractSymbols(now())
	requestSymbols := make([]string, len(symbols))
	for i, symbol := range symbols {
		requestSymbols[i] = "nf_" + strings.ToUpper(symbol)
	}
	address := "https://hq.sinajs.cn/list=" + strings.Join(requestSymbols, ",")
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, errors.New("构造公开报价请求失败")
	}
	req.Header.Set("User-Agent", "CornWatch/0.3 public research quotes")
	req.Header.Set("Referer", "https://finance.sina.com.cn/")
	client := c.Client
	if client == nil {
		client = New().Client
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, errors.New("公开报价网络错误或超时；应保留上次快照")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("公开报价 HTTP %d；应保留上次快照", resp.StatusCode)
	}
	if resp.Request != nil && resp.Request.URL != nil {
		u := resp.Request.URL
		if u.Scheme != "https" || u.Host != "hq.sinajs.cn" {
			return nil, errors.New("公开报价响应来源发生变化")
		}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil || len(body) > maxResponseBytes {
		return nil, errors.New("公开报价响应过大或读取失败")
	}
	return parseSina(body, symbols, now().UTC(), address)
}

func parseSina(body []byte, requested []string, received time.Time, sourceURL string) ([]Quote, error) {
	if len(body) == 0 || len(body) > maxResponseBytes || received.IsZero() {
		return nil, errors.New("公开报价响应为空、过大或采集时间无效")
	}
	u, err := url.Parse(sourceURL)
	if err != nil || u.Scheme != "https" || u.Host != "hq.sinajs.cn" {
		return nil, errors.New("公开报价来源无效")
	}
	decoded, err := simplifiedchinese.GB18030.NewDecoder().Bytes(body)
	if err != nil || !utf8.Valid(decoded) || strings.ContainsRune(string(decoded), '\ufffd') {
		return nil, errors.New("公开报价 GB18030 编码无效")
	}
	allowed := make(map[string]bool, len(requested))
	for _, s := range requested {
		if _, _, ok := contract(s); !ok {
			return nil, errors.New("请求中包含非实际合约")
		}
		allowed[strings.ToUpper(s)] = true
	}
	seen := map[string]bool{}
	products := map[string]int{}
	quotes := make([]Quote, 0, len(requested))
	for _, line := range strings.Split(strings.TrimSpace(string(decoded)), "\n") {
		match := assignmentPattern.FindStringSubmatch(strings.TrimSpace(line))
		if match == nil || !allowed[match[1]] || seen[match[1]] {
			return nil, errors.New("公开报价包装、合约或重复记录校验失败")
		}
		seen[match[1]] = true
		if match[2] == "" {
			continue
		}
		fields, err := csv.NewReader(strings.NewReader(match[2])).Read()
		if err != nil || (len(fields) != 28 && len(fields) != 44) {
			return nil, errors.New("公开报价字段版本不支持")
		}
		q, err := parseFields(strings.ToLower(match[1]), fields, received, sourceURL)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", match[1], err)
		}
		if q.Last == 0 {
			continue
		}
		quotes = append(quotes, q)
		products[q.Product]++
	}
	if len(seen) != len(allowed) {
		return nil, errors.New("公开报价缺失请求合约的响应")
	}
	for _, p := range []string{"c", "cs", "lh", "m"} {
		requestedProduct := false
		for _, s := range requested {
			product, _, _ := contract(s)
			if product == p {
				requestedProduct = true
				break
			}
		}
		if requestedProduct && products[p] == 0 {
			return nil, fmt.Errorf("公开报价缺少 %s 的有效实际合约", p)
		}
	}
	sort.Slice(quotes, func(i, j int) bool { return quotes[i].Symbol < quotes[j].Symbol })
	return quotes, nil
}

func parseFields(symbol string, f []string, received time.Time, address string) (Quote, error) {
	product, _, ok := contract(symbol)
	if !ok {
		return Quote{}, errors.New("非实际合约代码")
	}
	q := Quote{Symbol: symbol, Product: product, Name: f[0], Exchange: "DCE", TradeDate: f[17], ReceivedAt: received, Source: SourceName, SourceURL: address, ChangeBasis: "previous_settlement"}
	if f[0] == "" || len(f[0]) > 128 {
		return Quote{}, errors.New("合约名称无效")
	}
	var err error
	for _, item := range []struct {
		index int
		out   *float64
	}{{2, &q.Open}, {3, &q.High}, {4, &q.Low}, {8, &q.Last}, {10, &q.PreviousSettlement}} {
		*item.out, err = strconv.ParseFloat(f[item.index], 64)
		if err != nil || !finite(*item.out) || *item.out < 0 || *item.out > 1e9 {
			return Quote{}, errors.New("报价价格字段无效")
		}
	}
	// Unlisted/inactive contracts sometimes return a nonempty all-zero record.
	if q.Last == 0 && q.Open == 0 && q.High == 0 && q.Low == 0 {
		return q, nil
	}
	if q.Last <= 0 || q.Open <= 0 || q.Low <= 0 || q.PreviousSettlement <= 0 || q.High < math.Max(q.Open, q.Last) || q.Low > math.Min(q.Open, q.Last) || q.High < q.Low {
		return Quote{}, errors.New("公开报价 OHLC 或昨结不一致")
	}
	for _, item := range []struct {
		index int
		out   *int64
	}{{13, &q.OpenInterest}, {14, &q.Volume}} {
		n, err := strconv.ParseFloat(f[item.index], 64)
		if err != nil || !finite(n) || n < 0 || n > 1e12 || n != math.Trunc(n) {
			return Quote{}, errors.New("公开报价量仓字段无效")
		}
		*item.out = int64(n)
	}
	if len(f[1]) != 6 || len(f[17]) != 10 {
		return Quote{}, errors.New("公开报价日期时间无效")
	}
	q.ObservedAt, err = time.ParseInLocation("2006-01-02 150405", f[17]+" "+f[1], china)
	if err != nil || q.ObservedAt.After(received.Add(2*time.Minute)) {
		return Quote{}, errors.New("公开报价时间无效或来自未来")
	}
	q.ChangePct = (q.Last/q.PreviousSettlement - 1) * 100
	return q, nil
}

func contract(symbol string) (string, int, bool) {
	m := symbolPattern.FindStringSubmatch(strings.ToLower(symbol))
	if m == nil {
		return "", 0, false
	}
	year, _ := strconv.Atoi(m[2])
	month, _ := strconv.Atoi(m[3])
	if month < 1 || month > 12 || (month%2 == 0 && !(m[1] == "m" && (month == 8 || month == 12))) {
		return "", 0, false
	}
	return m[1], (2000+year)*100 + month, true
}

func finite(n float64) bool { return !math.IsNaN(n) && !math.IsInf(n, 0) }
