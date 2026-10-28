package store

import (
	"cornwatch/internal/domain"
	"math"
	"time"
)

// SeedDemo uses fixed, deliberately synthetic data. Dates are sample labels,
// not an exchange calendar. The function never downloads or implies live data.
func (s *Store) SeedDemo() error {
	var seeded bool
	if s.GetMeta("demo_v1", &seeded) == nil && seeded {
		return nil
	}
	loc := time.FixedZone("Asia/Shanghai", 8*3600)
	start := time.Date(2026, 5, 6, 15, 0, 0, 0, loc)
	bars := []domain.Bar{}
	for j, symbol := range []string{"c2701", "c2705", "cs2701", "lh2701", "m2701"} {
		base := []float64{2260, 2300, 2670, 13700, 2900}[j]
		prev := base
		index := 0
		for d := start; d.Before(time.Date(2026, 10, 27, 0, 0, 0, 0, loc)); d = d.AddDate(0, 0, 1) {
			if d.Weekday() == time.Saturday || d.Weekday() == time.Sunday {
				continue
			}
			if d.Month() == time.October && d.Day() <= 7 {
				continue
			}
			wave := 38*math.Sin(float64(index)/10) + 12*math.Sin(float64(index)/3.1)
			price := math.Round(base + float64(index)*0.35 + wave)
			open := prev + float64(index%5) - 2
			vol := int64(90000 + index*800 + j*20000)
			oi := int64(290000 + index*1400)
			if j == 1 {
				vol = vol / 3
				oi = oi / 2
			}
			bars = append(bars, domain.Bar{Symbol: symbol, Timestamp: d.UTC(), TradeDate: d.Format("2006-01-02"), Open: open, High: math.Max(open, price) + 8, Low: math.Min(open, price) - 9, Close: price, Settlement: math.Round((open + price) / 2), Volume: vol, OpenInterest: oi, LimitUp: math.Ceil(prev * 1.07), LimitDown: math.Floor(prev * .93), IsDemo: true})
			prev = price
			index++
		}
	}
	if _, _, err := s.ImportBars(bars, "synthetic-demo-v1"); err != nil {
		return err
	}
	t := time.Date(2026, 10, 26, 8, 0, 0, 0, loc)
	seeds := []struct{ title, body, cat, region, severity string }{
		{"山东深加工收购价调整 · 演示", "合成案例：某样例深加工企业玉米挂牌价由2260元/吨上调至2280元/吨。水分14%，厂门报价，非真实企业报价。", "purchase_price", "山东", "high"},
		{"东北降雨与收获进度 · 演示", "合成案例：吉林部分区域连续降雨，可能影响收获与公路运输。没有真实气象预警，不应据此判断减产。", "weather", "吉林", "medium"},
		{"晨间到车量变化 · 演示", "合成案例：同一统计样本的厂门到车量由昨日780辆降至620辆。样本口径未验证，仅用于演示事件处理。", "truck_arrivals", "山东", "medium"},
		{"淀粉—玉米价差观察 · 演示", "合成案例：淀粉价格上涨，生猪价格下跌。产业传导并不确定，需结合加工利润、开工率及饲料替代核验。", "related_price", "全国", "low"},
	}
	for i, e := range seeds {
		when := t.Add(time.Duration(i) * 30 * time.Minute)
		event := domain.Event{ID: ID("demo-v1", e.title), Title: e.title, Body: e.body, Category: e.cat, Region: e.region, Products: []string{"c", "cs", "lh"}, Source: "合成演示数据", PublishedAt: when, IngestedAt: when, AvailableAt: when, Severity: e.severity, IsDemo: true}
		if _, _, err := s.AddEvent(event); err != nil {
			return err
		}
	}
	obs := []domain.Observation{
		{Kind: "purchase_price", Region: "山东", Entity: "样例深加工厂", Value: 2280, Unit: "元/吨", Note: "合成数据；水分14%、厂门价、含税口径待验证"},
		{Kind: "purchase_price", Region: "吉林", Entity: "样例收购站", Value: 2190, Unit: "元/吨", Note: "合成数据；不代表真实收购价"},
		{Kind: "truck_arrivals", Region: "山东", Entity: "样例深加工企业样本", Value: 620, Unit: "辆", Note: "合成数据；同一时点待卸车辆，非成交量"},
		{Kind: "weather", Region: "吉林", Entity: "样例24小时累计降水", Value: 24, Unit: "mm", Note: "合成数据；不是气象预警"},
	}
	for i := range obs {
		obs[i].ID = ID("demo-v1", obs[i].Entity)
		obs[i].ObservedAt = t
		obs[i].AvailableAt = t
		obs[i].Source = "合成演示数据"
		obs[i].IsDemo = true
	}
	if _, err := s.AddObservations(obs); err != nil {
		return err
	}
	return s.SetMeta("demo_v1", true)
}
