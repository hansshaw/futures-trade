// Package currentanalysis combines separately timestamped research evidence.
// It never merges public quote samples into executable or backtestable bars.
package currentanalysis

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"time"

	"cornwatch/internal/datahub"
	"cornwatch/internal/domain"
	"cornwatch/internal/market"
	"cornwatch/internal/marketdaily"
	"cornwatch/internal/research"
)

const Version = "current-evidence-v1"

var cornContract = regexp.MustCompile(`^c[0-9]{2}(01|03|05|07|09|11)$`)
var china = time.FixedZone("Asia/Shanghai", 8*3600)

type Spread struct {
	Label      string    `json:"label"`
	Left       string    `json:"left"`
	Right      string    `json:"right"`
	Value      float64   `json:"value"`
	ObservedAt time.Time `json:"observed_at"`
	Meaning    string    `json:"meaning"`
}
type Factor struct {
	Title       string   `json:"title"`
	Body        string   `json:"body"`
	EvidenceIDs []string `json:"evidence_ids"`
}
type Scenario struct {
	Name           string `json:"name"`
	Condition      string `json:"condition"`
	Interpretation string `json:"interpretation"`
}
type Report struct {
	GeneratedAt      time.Time             `json:"generated_at"`
	Version          string                `json:"version"`
	InputSHA256      string                `json:"input_sha256"`
	Symbol           string                `json:"symbol"`
	Selection        string                `json:"selection"`
	Headline         string                `json:"headline"`
	Snapshot         market.Quote          `json:"snapshot"`
	MarketFreshness  string                `json:"market_freshness"`
	DailySource      string                `json:"daily_source"`
	DailyURL         string                `json:"daily_url"`
	DailyAvailableAt time.Time             `json:"daily_available_at"`
	DailySHA256      string                `json:"daily_sha256"`
	Technical        research.MarketReport `json:"technical"`
	Spreads          []Spread              `json:"spreads"`
	Factors          []Factor              `json:"factors"`
	Scenarios        []Scenario            `json:"scenarios"`
	Evidence         []domain.Observation  `json:"evidence"`
	Warnings         []string              `json:"warnings"`
}

func Select(live datahub.Live, now time.Time) (market.Quote, string, error) {
	choices := []market.Quote{}
	month := now.In(china).Year()*100 + int(now.In(china).Month())
	for _, q := range live.Quotes {
		if q.Exchange != "DCE" || q.Product != "c" || !cornContract.MatchString(q.Symbol) || q.Last <= 0 || math.IsNaN(q.Last) || math.IsInf(q.Last, 0) || q.ObservedAt.IsZero() || q.Source == "" || q.ObservedAt.After(now.Add(30*time.Second)) {
			continue
		}
		yy, e1 := strconv.Atoi(q.Symbol[1:3])
		mm, e2 := strconv.Atoi(q.Symbol[3:])
		if e1 != nil || e2 != nil || (2000+yy)*100+mm <= month {
			continue
		}
		choices = append(choices, q)
	}
	if len(choices) == 0 {
		return market.Quote{}, "", errors.New("先同步至少一个未进入交割月的实际玉米合约；分析不使用演示价格")
	}
	for _, entry := range []struct{ symbol, basis string }{{live.Main.Decision.Symbol, "按已确认研究主力"}, {live.Main.ProvisionalSymbol, "按当前候选合约；尚不等于已确认主力"}} {
		for _, q := range choices {
			if q.Symbol == entry.symbol {
				return q, entry.basis, nil
			}
		}
	}
	latestTime := choices[0].ObservedAt
	for _, q := range choices {
		if q.ObservedAt.After(latestTime) {
			latestTime = q.ObservedAt
		}
	}
	current := choices[:0]
	for _, q := range choices {
		if latestTime.Sub(q.ObservedAt) <= 5*time.Minute {
			current = append(current, q)
		}
	}
	choices = current
	sort.Slice(choices, func(i, j int) bool {
		if choices[i].Volume != choices[j].Volume {
			return choices[i].Volume > choices[j].Volume
		}
		if choices[i].OpenInterest != choices[j].OpenInterest {
			return choices[i].OpenInterest > choices[j].OpenInterest
		}
		return choices[i].Symbol < choices[j].Symbol
	})
	return choices[0], "按当前有效快照中成交量领先的玉米合约作研究参考；未确认主力", nil
}
func Build(live datahub.Live, daily marketdaily.Report, observations []domain.Observation, now time.Time) (Report, error) {
	q, selection, err := Select(live, now)
	if err != nil {
		return Report{}, err
	}
	if daily.AvailableAt.IsZero() || daily.AvailableAt.After(now) {
		return Report{}, errors.New("日线采集时间缺失或尚不可得")
	}
	if len(daily.Bars) == 0 || daily.Bars[len(daily.Bars)-1].Symbol != q.Symbol {
		return Report{}, errors.New("日线合约与当前分析合约不一致，请重新分析")
	}
	for _, b := range daily.Bars {
		if b.IsDemo || b.Timestamp.After(now) {
			return Report{}, errors.New("拒绝演示或未来日线")
		}
	}
	technical, err := research.AnalyzeObserved(daily.Bars, daily.HasOpenInterest)
	if err != nil {
		return Report{}, err
	}
	r := Report{GeneratedAt: now, Version: Version, Symbol: q.Symbol, Selection: selection, Snapshot: q, MarketFreshness: live.Freshness, DailySource: daily.Source, DailyURL: daily.SourceURL, DailyAvailableAt: daily.AvailableAt, DailySHA256: daily.SHA256, Technical: technical, Spreads: []Spread{}, Factors: []Factor{}, Scenarios: []Scenario{}, Evidence: []domain.Observation{}, Warnings: append([]string{}, daily.Warnings...)}
	r.Warnings = append(r.Warnings, "日线指标使用已完成的日K；快照显示原始自然时刻，夜盘不与当日日线成交量混算。公开源无交易级延迟保证。", "公开行业报价、晨间车辆样本和拍卖成交率各有口径；不自动折成基差、加工利润或全国进口总量。", "本报告为可核验规则整理，不是大模型预测；情景条件不是订单，不给出胜率或收益保证。")
	if live.Freshness == "stale" || live.Freshness == "unknown" || now.Sub(q.ObservedAt) > 96*time.Hour {
		r.Warnings = append(r.Warnings, "当前引用的市场快照陈旧或时效未确认，需先核验再判断盘面。")
	}
	if live.LatestDataAt != nil && live.LatestDataAt.Sub(q.ObservedAt) > 5*time.Minute {
		r.Warnings = append(r.Warnings, "所选合约报价落后于本批其他合约，相关比较需谨慎。")
	}
	if now.Sub(technical.AsOf) > 4*24*time.Hour {
		r.Warnings = append(r.Warnings, "最新完成日线距当前超过4天，可能休市、延迟或缺数；未用交易日历确认。")
	}
	latest := technical.Latest
	r.Headline = latest.Trend
	if latest.MA20 != nil && latest.MA60 != nil {
		if latest.Close > *latest.MA20 && latest.Close < *latest.MA60 {
			r.Headline = "中期均线下的短线修复，尚需收盘与产业证据确认"
		}
		if latest.Close < *latest.MA20 && latest.Close > *latest.MA60 {
			r.Headline = "中期均线上方的短线回撤，观察结构是否保持"
		}
	}
	lastPoint := technical.Points[len(technical.Points)-1]
	if latest.PriorHigh20 != nil && lastPoint.High > *latest.PriorHigh20 && lastPoint.Close <= *latest.PriorHigh20 {
		r.Factors = append(r.Factors, Factor{"突破是否成立", fmt.Sprintf("当日最高%.0f越过前20日高点%.0f，但收盘%.0f未站上；只能描述为盘中尝试突破，不能认定收盘突破。", lastPoint.High, *latest.PriorHigh20, lastPoint.Close), []string{}})
	}
	if latest.RSI14 != nil && *latest.RSI14 >= 70 {
		r.Headline += "；动量进入常用高位区，不能据此直接判定反转"
	}
	if latest.PriorHigh20 != nil && latest.PriorLow20 != nil {
		lo, hi := *latest.PriorLow20, *latest.PriorHigh20
		r.Scenarios = []Scenario{
			{"强势延续观察", fmt.Sprintf("后续完成日K维持在前20日高点 %.0f 上方，并核验量能、企业报价与到货是否配合。", hi), "突破仅是一项价格证据；若新粮到货回升或现货不跟涨，延续判断需要下调。"},
			{"回到震荡观察", fmt.Sprintf("突破后重回 %.0f 下方，或价格在 %.0f—%.0f 区间反复。", hi, lo, hi), "优先检查突破是否失败、移仓和事件扰动，不追认已发生的收益。"},
			{"转弱观察", fmt.Sprintf("后续完成日K跌破前20日低点 %.0f，且企业报价走弱、到货增加等独立证据同向。", lo), "只有价格和产业证据共振才增强转弱解释；缺少同口径数据时保留不确定性。"},
		}
	}
	compatible := func(other market.Quote) bool {
		return other.Last > 0 && !math.IsNaN(other.Last) && !math.IsInf(other.Last, 0) && !other.ObservedAt.IsZero() && !other.ObservedAt.After(now.Add(30*time.Second)) && other.Source == q.Source && other.TradeDate != "" && other.TradeDate == q.TradeDate && q.ObservedAt.Sub(other.ObservedAt).Abs() <= 5*time.Minute
	}
	far := []market.Quote{}
	for _, other := range live.Quotes {
		if !compatible(other) {
			continue
		}
		if other.Product == "c" && other.Symbol > q.Symbol {
			far = append(far, other)
		}
		if other.Symbol == "cs"+q.Symbol[1:] {
			r.Spreads = append(r.Spreads, Spread{"淀粉－玉米同月价差", other.Symbol, q.Symbol, other.Last - q.Last, q.ObservedAt, "仅同月期货价格差，不等于加工利润；尚未计入出粉率、副产品收益、能耗及费用。"})
		}
	}
	sort.Slice(far, func(i, j int) bool { return far[i].Volume > far[j].Volume })
	if len(far) > 0 {
		f := far[0]
		r.Spreads = append([]Spread{{"玉米近月－活跃远月", q.Symbol, f.Symbol, q.Last - f.Last, q.ObservedAt, "同源且时刻差不超过5分钟；负值表示远月升水，包含仓储、资金、季节性与供需预期，不是现货基差。"}}, r.Spreads...)
	}
	if len(r.Spreads) < 2 {
		r.Warnings = append(r.Warnings, "部分关联/远月合约缺少同来源、相近时刻报价，对应价差不计算。")
	}
	available := []domain.Observation{}
	for _, o := range observations {
		if !o.IsDemo && !o.AvailableAt.IsZero() && !o.AvailableAt.After(now) && !o.ObservedAt.After(now) && !math.IsNaN(o.Value) && !math.IsInf(o.Value, 0) {
			available = append(available, o)
		}
	}
	sort.Slice(available, func(i, j int) bool {
		if available[i].ObservedAt.Equal(available[j].ObservedAt) {
			return available[i].AvailableAt.After(available[j].AvailableAt)
		}
		return available[i].ObservedAt.After(available[j].ObservedAt)
	})
	seen := map[string]bool{}
	types := map[string]int{}
	sourceCounts := map[string]int{}
	for _, o := range available {
		if o.Kind == "weather" {
			continue
		}
		key := o.Kind + "|" + o.Region + "|" + o.Entity + "|" + o.Unit + "|" + o.Source
		if seen[key] || sourceCounts[o.Kind+"|"+o.Source] >= 4 || len(r.Evidence) >= 40 {
			continue
		}
		seen[key] = true
		types[o.Kind]++
		sourceCounts[o.Kind+"|"+o.Source]++
		r.Evidence = append(r.Evidence, o)
		age := now.Sub(o.ObservedAt)
		old := ""
		if age > 7*24*time.Hour {
			old = "该记录已超过7天，仅作历史背景。"
		}
		switch o.Kind {
		case "truck_arrivals":
			evidenceIDs := []string{o.ID}
			body := fmt.Sprintf("%s，%s晨间/原报表口径 %.0f %s。%s %s", o.ObservedAt.In(china).Format("01月02日"), o.Region, o.Value, o.Unit, o.Note, old)
			for _, p := range available {
				if p.Kind == o.Kind && p.Region == o.Region && p.Entity == o.Entity && p.Unit == o.Unit && p.Source == o.Source && p.ObservedAt.Before(o.ObservedAt) {
					body += fmt.Sprintf(" 较同序列上一公开记录（%s）变化%+.0f %s；样本和统计窗是否恒定仍须核验。", p.ObservedAt.In(china).Format("01-02"), o.Value-p.Value, o.Unit)
					evidenceIDs = append(evidenceIDs, p.ID)
					found := false
					for _, saved := range r.Evidence {
						if saved.ID == p.ID {
							found = true
							break
						}
					}
					if !found {
						r.Evidence = append(r.Evidence, p)
					}
					break
				}
			}
			r.Factors = append(r.Factors, Factor{"到车与短时供应", body + " 到车变化可影响厂门短时供应，不等于全国供给或已卸粮吨数。", evidenceIDs})
		case "import_auction_rate":
			r.Factors = append(r.Factors, Factor{"进口存量拍卖参考", fmt.Sprintf("最近公开场次%s，成交率%.2f%%。这是境内进口存量竞价销售，不能代表当天新签海外采购或海关进口量；缺少计划吨数、成交吨数与价格时不反推。%s", o.ObservedAt.In(china).Format("2006-01-02"), o.Value, old), []string{o.ID}})
		}
	}
	known, unknown := 0, 0
	ids := []string{}
	for _, o := range r.Evidence {
		if o.Kind == "purchase_price" {
			ids = append(ids, o.ID)
			if o.Unit == "元/吨" || o.Unit == "元/公斤" {
				known++
			} else {
				unknown++
			}
		}
	}
	if known+unknown > 0 {
		r.Factors = append(r.Factors, Factor{"收购端证据与口径", fmt.Sprintf("当前引用%d条有明确价格单位的监测/企业报价、%d条单位未确认的公开表格值。后者只展示原值；未核实水分、等级、税费、地点和运费前，不与期货直接计算基差。", known, unknown), ids})
	}
	if types["import_auction_rate"] == 0 {
		r.Warnings = append(r.Warnings, "本地尚无进口玉米拍卖结果，不能把未公告或未同步解释成零成交。")
	}
	if types["truck_arrivals"] == 0 {
		r.Warnings = append(r.Warnings, "本地尚无可用到车记录，短期供给解释缺少该项证据。")
	}
	input, _ := json.Marshal(struct {
		Live     datahub.Live
		Daily    marketdaily.Report
		Evidence []domain.Observation
	}{live, daily, r.Evidence})
	sum := sha256.Sum256(input)
	r.InputSHA256 = hex.EncodeToString(sum[:])
	// No event text, browser instruction or language-model output is executed.
	return r, nil
}
