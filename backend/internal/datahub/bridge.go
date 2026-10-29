package datahub

import (
	"context"
	"cornwatch/internal/domain"
	"cornwatch/internal/market"
	"cornwatch/internal/store"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var actualContract = regexp.MustCompile(`^(c|cs|lh|m)([0-9]{2})(01|03|05|07|08|09|11|12)$`)

type bridgePayload struct {
	GeneratedAt  time.Time            `json:"generated_at"`
	Quotes       []market.Quote       `json:"quotes"`
	Observations []domain.Observation `json:"observations"`
}

func fetchBridge(ctx context.Context, client *http.Client, c Config, isMarket bool, now time.Time) (Batch, error) {
	started := time.Now()
	return fetchBridgeWithClock(ctx, client, c, isMarket, func() time.Time { return now.Add(time.Since(started)) })
}

func fetchBridgeWithClock(ctx context.Context, client *http.Client, c Config, isMarket bool, clock func() time.Time) (Batch, error) {
	if err := validateEndpoint(c.Endpoint); err != nil {
		return Batch{}, err
	}
	request, err := http.NewRequestWithContext(ctx, "GET", c.Endpoint, nil)
	if err != nil {
		return Batch{}, errors.New("桥接地址无效")
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "CornWatch/0.3 data-bridge")
	if c.Token != "" {
		request.Header.Set("Authorization", "Bearer "+c.Token)
	}
	response, err := client.Do(request)
	if err != nil {
		return Batch{}, errors.New("桥接连接失败或超时，请检查服务地址及运行状态")
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return Batch{}, fmt.Errorf("桥接返回HTTP %d；401/403表示凭据或权限问题", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 4*1024*1024+1))
	if err != nil || len(body) > 4*1024*1024 {
		return Batch{}, errors.New("桥接响应读取失败或超过4MiB")
	}
	// Acquisition occurs after the response body arrives, not when its request
	// began. A quote produced during network transit must not appear to have
	// been received before its own observed_at when received_at was omitted.
	now := clock()
	var payload bridgePayload
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&payload); err != nil {
		return Batch{}, errors.New("桥接JSON格式不符合项目契约，请检查quotes/observations字段")
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return Batch{}, errors.New("桥接响应必须是一个JSON对象")
	}
	// A fresh HTTP response alone is not evidence that the SDK worker is alive.
	// Market bridges must date each healthy response independently of quotes,
	// which legitimately keep their original receive time after the close.
	if isMarket && payload.GeneratedAt.IsZero() {
		return Batch{}, errors.New("行情桥接必须提供generated_at以核验本次响应生成时间")
	}
	if !payload.GeneratedAt.IsZero() && (now.Sub(payload.GeneratedAt) > 2*time.Minute || payload.GeneratedAt.After(now.Add(30*time.Second))) {
		return Batch{}, errors.New("桥接generated_at已过期或晚于当前时间，请检查进程健康及本机时钟")
	}
	if isMarket {
		if len(payload.Quotes) == 0 || len(payload.Quotes) > 200 || len(payload.Observations) > 0 {
			return Batch{}, errors.New("行情桥接需要1至200条quotes，不能混入产业记录")
		}
		seen := map[string]bool{}
		for i := range payload.Quotes {
			q := &payload.Quotes[i]
			q.Symbol = strings.ToLower(q.Symbol)
			parts := actualContract.FindStringSubmatch(q.Symbol)
			if parts == nil || parts[1] != "m" && (parts[3] == "08" || parts[3] == "12") || seen[q.Symbol] {
				return Batch{}, errors.New("行情桥接包含重复或不支持的实际合约；仅c/cs/lh/m，不接受主连代码")
			}
			seen[q.Symbol] = true
			if q.Product != "" && q.Product != parts[1] {
				return Batch{}, errors.New("合约与品种代码不一致")
			}
			q.Product = parts[1]
			if q.Exchange != "" && q.Exchange != "DCE" {
				return Batch{}, errors.New("当前桥接仅支持DCE品种")
			}
			q.Exchange = "DCE"
			if q.Last <= 0 || q.PreviousSettlement <= 0 || q.Volume < 0 || q.OpenInterest < 0 || q.ObservedAt.IsZero() || q.ObservedAt.After(now.Add(30*time.Second)) {
				return Batch{}, errors.New("行情价格、量仓或源时间无效")
			}
			for _, v := range []float64{q.Last, q.Open, q.High, q.Low, q.PreviousSettlement} {
				if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > 1e9 {
					return Batch{}, errors.New("行情数值超出有效范围")
				}
			}
			if q.TradeDate == "" {
				q.TradeDate = q.ObservedAt.In(time.FixedZone("Asia/Shanghai", 8*3600)).Format("2006-01-02")
			}
			if _, err := time.Parse("2006-01-02", q.TradeDate); err != nil {
				return Batch{}, errors.New("行情日期格式无效")
			}
			producerReceived := !q.ReceivedAt.IsZero()
			if !producerReceived {
				// A missing producer timestamp is explicitly documented as a local
				// acquisition timestamp, never as the SDK's original receive time.
				q.ReceivedAt = now
			}
			if q.ReceivedAt.Before(q.ObservedAt) || q.ReceivedAt.After(now.Add(30*time.Second)) || producerReceived && q.ReceivedAt.After(payload.GeneratedAt.Add(30*time.Second)) {
				return Batch{}, errors.New("行情received_at不能早于observed_at或晚于本次响应/当前时间")
			}
			q.ChangePct = (q.Last/q.PreviousSettlement - 1) * 100
			if math.IsNaN(q.ChangePct) || math.IsInf(q.ChangePct, 0) {
				return Batch{}, errors.New("行情涨跌幅计算超出有效范围")
			}
			q.ChangeBasis = "previous_settlement"
			q.Source = "用户配置的授权行情桥接"
			q.SourceURL = c.Endpoint
			if len(q.Name) > 100 {
				return Batch{}, errors.New("合约名称过长")
			}
			if q.Name == "" {
				q.Name = map[string]string{"c": "玉米", "cs": "玉米淀粉", "lh": "生猪", "m": "豆粕"}[q.Product]
			}
		}
		return quoteBatch(payload.Quotes), nil
	}
	if len(payload.Observations) == 0 || len(payload.Observations) > 1000 || len(payload.Quotes) > 0 {
		return Batch{}, errors.New("产业桥接需要1至1000条observations，不能混入行情")
	}
	for i := range payload.Observations {
		o := &payload.Observations[i]
		if o.Kind != "purchase_price" && o.Kind != "truck_arrivals" && o.Kind != "related_price" {
			return Batch{}, errors.New("产业类型仅支持purchase_price/truck_arrivals/related_price")
		}
		if o.IsDemo || strings.TrimSpace(o.Entity) == "" || strings.TrimSpace(o.Region) == "" || strings.TrimSpace(o.Source) == "" || strings.TrimSpace(o.Note) == "" || strings.TrimSpace(o.Unit) == "" {
			return Batch{}, errors.New("产业记录必须有主体、地区、来源、单位和统计口径，不能将演示标为真实")
		}
		if len(o.Entity) > 200 || len(o.Region) > 100 || len(o.Source) > 200 || len(o.Note) > 4000 || len(o.Unit) > 40 || len(o.SourceURL) > 2000 {
			return Batch{}, errors.New("产业记录字段过长")
		}
		if math.IsNaN(o.Value) || math.IsInf(o.Value, 0) || o.Value < 0 || o.Value > 1e9 || o.Kind != "truck_arrivals" && o.Value == 0 || o.Kind == "truck_arrivals" && math.Trunc(o.Value) != o.Value {
			return Batch{}, errors.New("产业数值无效；价格需为正，到车数需为非负整数")
		}
		if o.Kind == "truck_arrivals" {
			if o.Unit != "辆" && o.Unit != "车" {
				return Batch{}, errors.New("到车数的单位必须为辆或车，不能将吨数当作车辆数")
			}
			o.Unit = "辆"
		}
		if o.ObservedAt.IsZero() || o.ObservedAt.After(now.Add(5*time.Minute)) {
			return Batch{}, errors.New("产业观测时间缺失或晚于当前时间")
		}
		if o.SourceURL != "" {
			u, parseErr := url.Parse(o.SourceURL)
			if parseErr != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || strings.ContainsAny(o.SourceURL, "\r\n\x00") {
				return Batch{}, errors.New("来源链接必须是有主机名且不含凭据的完整HTTP或HTTPS地址")
			}
		}
		o.ID = store.ID("industry-bridge", datasetBinding(c), o.Kind, o.Region, o.Entity, o.ObservedAt.UTC().Format(time.RFC3339Nano), strconv.FormatFloat(o.Value, 'g', -1, 64), o.Unit, o.Note, o.Source)
		o.AvailableAt = now
		o.Source = "授权桥接 / " + o.Source
	}
	return observationBatch(payload.Observations, nil), nil
}
