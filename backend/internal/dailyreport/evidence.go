package dailyreport

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"cornwatch/internal/currentanalysis"
	"cornwatch/internal/domain"
	"cornwatch/internal/market"
)

var china = time.FixedZone("Asia/Shanghai", 8*3600)

// Build freezes evidence known at now. Date is a report label, not a substitute
// for the evidence cutoff; this API cannot reconstruct a past report from data
// first collected later. Current must have been built from completed daily bars.
func Build(kind, date string, current *currentanalysis.Report, observations []domain.Observation, events []domain.Event, now time.Time) (Bundle, error) {
	return BuildWithQuotes(kind, date, current, observations, events, nil, now)
}

func BuildWithQuotes(kind, date string, current *currentanalysis.Report, observations []domain.Observation, events []domain.Event, quotes []market.Quote, now time.Time) (Bundle, error) {
	if err := validLabel(kind, date, now); err != nil {
		return Bundle{}, err
	}
	if err := validCurrent(current, now); err != nil {
		return Bundle{}, err
	}
	raw, err := json.Marshal(current)
	if err != nil || len(raw) > maxBundleBytes/2 {
		return Bundle{}, errors.New("当前分析快照无效或过大")
	}
	var frozen currentanalysis.Report
	if json.Unmarshal(raw, &frozen) != nil {
		return Bundle{}, errors.New("当前分析快照无法复制")
	}
	b := Bundle{AsOf: now.UTC(), Kind: kind, Date: date, Symbol: current.Symbol, Current: &frozen, Evidence: []Evidence{}, Warnings: []string{}}
	b.Warnings = append(b.Warnings, "报告仅使用截止时刻已采集的资料；统计日、发布日期、首次可用时间与报告日期不能互换。", "公开资料可能延迟或不完整；条件推断不是交易指令、因果结论或收益保证。")
	for _, w := range current.Warnings {
		b.warn(w)
	}
	if date != now.In(china).Format("2006-01-02") {
		b.warn("报告标签日期与资料截止日不同；本报告不是该标签日期的历史信息回放。")
	}

	// Keep the selected contract first. Related contracts are independently
	// timestamped observations; we do not manufacture simultaneous spread legs.
	allQuotes := append([]market.Quote{current.Snapshot}, quotes...)
	quoteLatest := map[string]market.Quote{}
	for i, q := range allQuotes {
		if !validQuote(q, now) {
			continue
		}
		if i > 0 && q.Symbol == current.Symbol {
			continue
		}
		key := q.Source + "\x00" + q.Symbol
		p, ok := quoteLatest[key]
		if !ok || q.ObservedAt.After(p.ObservedAt) || (q.ObservedAt.Equal(p.ObservedAt) && q.ReceivedAt.After(p.ReceivedAt)) {
			quoteLatest[key] = q
		}
	}
	qEvidence := []Evidence{}
	for _, q := range quoteLatest {
		category := "related_futures"
		if q.Symbol == current.Symbol {
			category = "market"
		}
		encoded, _ := json.Marshal(q)
		e := Evidence{ID: "market:" + digest(encoded), Category: category, Title: q.Symbol + " 公开期货快照", Source: q.Source, SourceURL: q.SourceURL, ObservedAt: q.ObservedAt, AvailableAt: q.ReceivedAt, Unit: "元/吨"}
		e.Text = fmt.Sprintf("%s 最新价 %g 元/吨；成交量 %d 手；持仓量 %d 手；原始交易日期 %s；行情时间 %s；接收时间 %s。", q.Symbol, q.Last, q.Volume, q.OpenInterest, q.TradeDate, stamp(q.ObservedAt), stamp(q.ReceivedAt))
		if q.PreviousSettlement > 0 {
			e.Text += fmt.Sprintf("参考基准价 %g；变动 %.3f%%；基准说明：%s。", q.PreviousSettlement, q.ChangePct, clean(q.ChangeBasis, 160))
		}
		e.Note = age(q.ObservedAt, now) + "公开快照不是逐笔行情或最终结算；跨来源、不同日期或不同步价格不可直接计算价差。"
		if category == "market" {
			e.Note += " 合约选择依据：" + clean(current.Selection, 240)
		}
		qEvidence = append(qEvidence, e)
	}
	sortEvidence(qEvidence)
	for _, e := range qEvidence {
		if e.Category == "market" {
			b.add(e)
		}
	}
	related := []Evidence{}
	for _, e := range qEvidence {
		if e.Category != "market" {
			related = append(related, e)
		}
	}
	for _, e := range balanced(related, 6, func(e Evidence) string {
		if strings.HasPrefix(e.Title, "cs") {
			return "cs"
		}
		if strings.HasPrefix(e.Title, "lh") {
			return "lh"
		}
		if strings.HasPrefix(e.Title, "m") {
			return "m"
		}
		return "c"
	}) {
		if e.Category != "market" {
			b.add(e)
		}
	}

	t := current.Technical
	te := Evidence{ID: "daily:" + current.InputSHA256, Category: "technical", Title: current.Symbol + " 已完成日线研究", Source: current.DailySource, SourceURL: current.DailyURL, ObservedAt: t.AsOf, AvailableAt: current.DailyAvailableAt, Unit: "元/吨"}
	if current.InputSHA256 == "" {
		te.ID = "daily:" + digest(raw)
	}
	te.Text = fmt.Sprintf("已完成日线截至 %s，共 %d 根；收盘价 %g 元/吨；趋势描述：%s；价量持仓描述：%s。", stamp(t.AsOf), t.BarCount, t.Latest.Close, clean(t.Latest.Trend, 240), clean(t.Latest.PriceOILabel, 240))
	for _, v := range []struct {
		name  string
		value *float64
	}{{"MA20", t.Latest.MA20}, {"MA60", t.Latest.MA60}, {"ATR14", t.Latest.ATR14}, {"RSI14", t.Latest.RSI14}, {"前20日高点", t.Latest.PriorHigh20}, {"前20日低点", t.Latest.PriorLow20}, {"20日成交量比", t.Latest.VolumeRatio20}} {
		if v.value != nil {
			te.Text += fmt.Sprintf("%s=%g；", v.name, *v.value)
		}
	}
	for i, in := range t.Insights {
		if i == 3 {
			break
		}
		te.Text += " " + clean(in.Title+"："+in.Body, 350)
	}
	te.Note = age(t.AsOf, now) + "指标只来自currentanalysis提供的已完成日K；不将夜盘或盘中最新价当作当日收盘价。"
	b.add(te)

	// A newly fetched forecast supersedes only the same source/region/target
	// date/metric. Future targets remain forecasts, never realized weather.
	groups := map[string][]domain.Observation{}
	for _, o := range observations {
		forecast := isForecast(o)
		if o.ID == "" || o.IsDemo || o.Source == "" || !finite(o.Value) || !known(o.AvailableAt, now) || o.ObservedAt.IsZero() || (!forecast && o.ObservedAt.After(now)) {
			continue
		}
		if forecast && (o.ObservedAt.Before(day(now)) || o.ObservedAt.After(day(now).AddDate(0, 0, 7))) {
			continue
		}
		key := strings.Join([]string{o.Kind, o.Region, o.Entity, o.Unit, o.Source}, "\x00")
		if forecast {
			key += "\x00" + o.ObservedAt.In(china).Format("2006-01-02")
		}
		groups[key] = append(groups[key], o)
	}
	byCategory := map[string][]Evidence{}
	for _, entries := range groups {
		sort.Slice(entries, func(i, j int) bool {
			if isForecast(entries[i]) {
				if !entries[i].AvailableAt.Equal(entries[j].AvailableAt) {
					return entries[i].AvailableAt.After(entries[j].AvailableAt)
				}
			} else if !entries[i].ObservedAt.Equal(entries[j].ObservedAt) {
				return entries[i].ObservedAt.After(entries[j].ObservedAt)
			}
			if !entries[i].AvailableAt.Equal(entries[j].AvailableAt) {
				return entries[i].AvailableAt.After(entries[j].AvailableAt)
			}
			return entries[i].ID < entries[j].ID
		})
		chosen := []domain.Observation{entries[0]}
		if entries[0].Kind == "truck_arrivals" {
			for _, p := range entries[1:] {
				if p.ObservedAt.Before(entries[0].ObservedAt) {
					chosen = append(chosen, p)
					break
				}
			}
		}
		for _, o := range chosen {
			category := categoryFor(o.Kind)
			forecast := isForecast(o)
			if forecast {
				category = "weather_forecast"
			}
			unit := strings.TrimSpace(o.Unit)
			if unit == "" {
				unit = "未注明"
			}
			e := Evidence{ID: "obs:" + o.ID, OriginalIDs: []string{o.ID}, Category: category, Title: clean(o.Region+" · "+o.Entity, 200), Source: clean(o.Source, 200), SourceURL: clean(o.SourceURL, 2000), ObservedAt: o.ObservedAt, AvailableAt: o.AvailableAt, IsForecast: forecast, Unit: unit}
			e.Text = fmt.Sprintf("%s；数值 %g；单位：%s；统计/目标时点 %s；首次可用 %s。", e.Title, o.Value, unit, stamp(o.ObservedAt), stamp(o.AvailableAt))
			e.Note = clean(o.Note, 1100)
			if forecast {
				e.Text = "天气预报（不是已发生事实）：" + e.Text
				e.Note += " 采用截止时刻该地区、目标日和指标最新可得的预报版本；点位不代表整个省。"
			} else {
				e.Note += " " + age(o.ObservedAt, now)
			}
			if unit == "未注明" {
				e.Note += " 原始来源未注明单位，保留原值；禁止猜测元/吨或元/斤，禁止直接计算基差、套利或跨来源价格涨跌。"
			}
			if category == "truck_arrivals" {
				e.Note += " 车辆口径以原文为准；晨间到车、待卸排队、全天到货与卸粮吨数不得混同。"
			}
			if category == "import" {
				e.Note += " 区分进口存量拍卖、海关进口量、海外新签采购与到港；不能互相代替。"
			}
			byCategory[category] = append(byCategory[category], e)
		}
	}
	pending := map[string][]Evidence{}
	categoryOrder := []string{"purchase_price", "truck_arrivals", "weather_forecast", "weather", "import", "related_price", "industry", "event"}
	for _, category := range categoryOrder[:len(categoryOrder)-1] {
		items := byCategory[category]
		if category == "weather_forecast" {
			items = combineForecasts(items)
		}
		sortEvidence(items)
		if category == "weather_forecast" {
			sort.SliceStable(items, func(i, j int) bool {
				if !items[i].ObservedAt.Equal(items[j].ObservedAt) {
					return items[i].ObservedAt.Before(items[j].ObservedAt)
				}
				return items[i].ID < items[j].ID
			})
		}
		limit := map[string]int{"purchase_price": 10, "truck_arrivals": 4, "weather_forecast": 15, "weather": 2, "import": 4, "related_price": 6, "industry": 2}[category]
		selected := balanced(items, limit, func(e Evidence) string {
			if category == "weather_forecast" {
				return e.Source + "\x00" + strings.SplitN(e.Title, " · ", 2)[0]
			}
			return e.Source + "\x00" + e.Unit
		})
		pending[category] = selected
		if len(items) > len(selected) {
			b.warn(category + "证据已按来源与口径限量取样，未覆盖全部企业或地区。")
		}
	}
	news := []Evidence{}
	for _, e := range events {
		if e.ID == "" || e.IsDemo || e.Source == "" || !known(e.AvailableAt, now) || !known(e.PublishedAt, now) {
			continue
		}
		if e.PublishedAt.Before(now.Add(-30 * 24 * time.Hour)) {
			continue
		}
		forecast := e.Category == "weather" && strings.Contains(e.Title+e.Body, "预报")
		news = append(news, Evidence{ID: "event:" + e.ID, OriginalIDs: []string{e.ID}, Category: "event", Title: clean(e.Title, 240), Text: clean(e.Body, 1800), Source: clean(e.Source, 200), SourceURL: clean(e.SourceURL, 2000), ObservedAt: e.PublishedAt, AvailableAt: e.AvailableAt, IsForecast: forecast, Note: "已公布信息；具体生效日、执行条件和预报目标日请按原文核验。公告不等于已生效或已实际执行。原文类别：" + clean(e.Category, 80) + "。" + age(e.PublishedAt, now)})
	}
	sortEvidence(news)
	pending["event"] = balanced(news, 8, func(e Evidence) string { return e.Source })
	if len(news) > len(pending["event"]) {
		b.warn("公告已按来源限量取样，未覆盖所有历史公告。")
	}
	// Round-robin across categories also protects the text budget: many long
	// enterprise notes must not crowd out all weather/import/pending notices.
	for i := 0; i < 15; i++ {
		for _, category := range categoryOrder {
			if i < len(pending[category]) {
				b.add(pending[category][i])
			}
		}
	}
	for _, c := range []struct{ key, label string }{{"market", "行情"}, {"purchase_price", "企业/监测收购价"}, {"truck_arrivals", "到车/待卸"}, {"weather_forecast", "天气预报"}, {"import", "进口相关资料"}, {"related_price", "关联现货价格"}} {
		found := false
		for _, e := range b.Evidence {
			if e.Category == c.key {
				found = true
				break
			}
		}
		if !found {
			b.warn("缺少截止时刻有效的" + c.label + "证据；缺数不是零值或无变化。")
		}
	}
	if err = b.seal(); err != nil {
		return Bundle{}, err
	}
	if err = validateBundle(b); err != nil {
		return Bundle{}, err
	}
	return b, nil
}

func validLabel(kind, date string, now time.Time) error {
	if kind != "pre" && kind != "post" {
		return errors.New("报告类型必须为pre或post")
	}
	d, err := time.ParseInLocation("2006-01-02", date, china)
	if err != nil || d.Format("2006-01-02") != date || now.IsZero() {
		return errors.New("报告日期或截止时刻无效")
	}
	return nil
}
func validCurrent(c *currentanalysis.Report, now time.Time) error {
	if c == nil || c.Symbol == "" || c.DailySource == "" || c.Symbol != c.Snapshot.Symbol || c.Symbol != c.Technical.Symbol || c.Technical.IsDemo || !finite(c.Technical.Latest.Close) || c.Technical.Latest.Close <= 0 || !known(c.GeneratedAt, now) || !known(c.DailyAvailableAt, now) || !validQuote(c.Snapshot, now) || !known(c.Technical.AsOf, now) || c.Technical.BarCount < 1 || len(c.Technical.Points) < 1 {
		return errors.New("请先生成包含真实行情与已完成日线的当前分析；资料时刻必须不晚于截止时刻")
	}
	for _, p := range c.Technical.Points {
		d, err := time.ParseInLocation("2006-01-02", p.TradeDate, china)
		if !known(p.Timestamp, now) || err != nil || now.Before(d.Add(15*time.Hour+15*time.Minute)) {
			return errors.New("当前分析含尚未完成或无有效日期的日线")
		}
	}
	for _, o := range c.Evidence {
		if o.IsDemo || !known(o.AvailableAt, now) || !known(o.ObservedAt, now) {
			return errors.New("当前分析快照包含截止时刻不可得的产业资料")
		}
	}
	for _, s := range c.Spreads {
		if !known(s.ObservedAt, now) {
			return errors.New("当前分析价差时刻无效")
		}
	}
	return nil
}
func validQuote(q market.Quote, now time.Time) bool {
	return q.Source != "" && !strings.Contains(strings.ToLower(q.Source), "demo") && !strings.Contains(q.Source, "演示") && q.Exchange == "DCE" && (q.Product == "c" || q.Product == "cs" || q.Product == "m" || q.Product == "lh") && q.Symbol != "" && q.Last > 0 && finite(q.Last) && finite(q.PreviousSettlement) && finite(q.ChangePct) && known(q.ObservedAt, now) && known(q.ReceivedAt, now) && !q.ObservedAt.After(q.ReceivedAt)
}
func isForecast(o domain.Observation) bool {
	return o.Kind == "weather" && (strings.Contains(o.Entity+o.Note, "预报") || strings.Contains(o.Entity+o.Note, "预测") || o.Source == "Open-Meteo")
}
func categoryFor(kind string) string {
	switch kind {
	case "purchase_price", "truck_arrivals", "weather", "related_price":
		return kind
	}
	if strings.Contains(kind, "import") || strings.Contains(kind, "auction") {
		return "import"
	}
	return "industry"
}
func known(t, now time.Time) bool { return !t.IsZero() && !t.After(now) }
func finite(v float64) bool       { return !math.IsNaN(v) && !math.IsInf(v, 0) }
func day(t time.Time) time.Time {
	v := t.In(china)
	return time.Date(v.Year(), v.Month(), v.Day(), 0, 0, 0, 0, china)
}
func stamp(t time.Time) string { return t.In(china).Format("2006-01-02 15:04:05 -07:00") }
func age(t, now time.Time) string {
	return fmt.Sprintf("统计/行情时点距截止约 %.1f 小时；请核验休市、发布周期与延迟，非实时保证。", now.Sub(t).Hours())
}
func clean(s string, n int) string {
	s = strings.TrimSpace(strings.ToValidUTF8(s, ""))
	r := []rune(s)
	if len(r) > n {
		return string(r[:n]) + "…[原文截取]"
	}
	return s
}
func digest(raw []byte) string { h := sha256.Sum256(raw); return hex.EncodeToString(h[:]) }
func sortEvidence(e []Evidence) {
	sort.Slice(e, func(i, j int) bool {
		if !e[i].ObservedAt.Equal(e[j].ObservedAt) {
			return e[i].ObservedAt.After(e[j].ObservedAt)
		}
		if !e[i].AvailableAt.Equal(e[j].AvailableAt) {
			return e[i].AvailableAt.After(e[j].AvailableAt)
		}
		return e[i].ID < e[j].ID
	})
}

// Group a region's target-day metrics without discarding tomorrow's rain just
// because today's temperature also consumes a slot. The underlying observation
// IDs and each metric's availability remain auditable. Different first-seen
// times are not represented as a single meteorological issuance timestamp.
func combineForecasts(items []Evidence) []Evidence {
	groups := map[string][]Evidence{}
	for _, e := range items {
		region := strings.SplitN(e.Title, " · ", 2)[0]
		key := e.Source + "\x00" + region + "\x00" + e.ObservedAt.In(china).Format("2006-01-02")
		groups[key] = append(groups[key], e)
	}
	out := []Evidence{}
	for _, metrics := range groups {
		sort.Slice(metrics, func(i, j int) bool { return metrics[i].ID < metrics[j].ID })
		first := metrics[0]
		raw, _ := json.Marshal(metrics)
		e := Evidence{ID: "weather:" + digest(raw), Category: "weather_forecast", Title: strings.SplitN(first.Title, " · ", 2)[0] + " · " + first.ObservedAt.In(china).Format("2006-01-02") + " 天气预报", Source: first.Source, SourceURL: first.SourceURL, ObservedAt: first.ObservedAt, AvailableAt: first.AvailableAt, IsForecast: true, Unit: "各指标分别注明", OriginalIDs: []string{}, Note: "各指标采用截止时刻最新可得版本；每项首次可用时间分别保留，不等同气象机构统一发布时间。预报不是已发生事实，点位不代表全省。"}
		for i, m := range metrics {
			if i == 6 {
				e.Note += " 指标超过6项，仅保留部分指标。"
				break
			}
			if e.AvailableAt.Before(m.AvailableAt) {
				e.AvailableAt = m.AvailableAt
			}
			e.Text += m.Text + "\n"
			e.OriginalIDs = append(e.OriginalIDs, m.OriginalIDs...)
		}
		e.Text = strings.TrimSpace(e.Text)
		out = append(out, e)
	}
	return out
}
func balanced(items []Evidence, limit int, key func(Evidence) string) []Evidence {
	groups := map[string][]Evidence{}
	for _, e := range items {
		groups[key(e)] = append(groups[key(e)], e)
	}
	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := []Evidence{}
	for index := 0; len(out) < limit; index++ {
		added := false
		for _, k := range keys {
			if index < len(groups[k]) && len(out) < limit {
				out = append(out, groups[k][index])
				added = true
			}
		}
		if !added {
			break
		}
	}
	return out
}
func (b *Bundle) warn(s string) {
	s = clean(s, 500)
	if s == "" {
		return
	}
	for _, w := range b.Warnings {
		if w == s {
			return
		}
	}
	if len(b.Warnings) < 30 {
		b.Warnings = append(b.Warnings, s)
	}
}
func (b *Bundle) add(e Evidence) {
	if len(b.Evidence) >= MaxEvidence {
		b.warn("证据数量达到上限，仅保留有限代表样本。")
		return
	}
	n := utf8.RuneCountInString(e.Text + e.Note)
	for _, v := range b.Evidence {
		if v.ID == e.ID {
			return
		}
		n += utf8.RuneCountInString(v.Text + v.Note)
	}
	if n > maxEvidenceText {
		b.warn("证据正文达到长度上限，部分资料未进入本次报告。")
		return
	}
	b.Evidence = append(b.Evidence, e)
}
func (b *Bundle) seal() error {
	b.InputSHA256 = ""
	raw, err := json.Marshal(b)
	if err != nil || len(raw) > maxBundleBytes {
		return errors.New("证据快照无效或超过1MiB限制")
	}
	b.InputSHA256 = digest(raw)
	return nil
}

func validateBundle(b Bundle) error {
	if err := validLabel(b.Kind, b.Date, b.AsOf); err != nil {
		return err
	}
	if len(b.Evidence) < 1 || len(b.Evidence) > MaxEvidence || b.Symbol == "" || len(b.Warnings) > 30 {
		return errors.New("证据快照为空或超限")
	}
	ids := map[string]bool{}
	count := 0
	for _, e := range b.Evidence {
		count += utf8.RuneCountInString(e.Text + e.Note)
		if e.ID == "" || ids[e.ID] || e.Category == "" || !validText(e.Title, 1200) || !validText(e.Text, 12000) || e.Source == "" || !known(e.AvailableAt, b.AsOf) || e.ObservedAt.IsZero() || (!e.IsForecast && e.ObservedAt.After(b.AsOf)) {
			return errors.New("证据内容、引用标识或时点无效")
		}
		ids[e.ID] = true
	}
	if count > maxEvidenceText {
		return errors.New("证据正文超过长度限制")
	}
	want := b.InputSHA256
	if err := b.seal(); err != nil {
		return err
	}
	if want == "" || want != b.InputSHA256 {
		return errors.New("证据快照哈希不匹配，请重新建立报告")
	}
	return nil
}
