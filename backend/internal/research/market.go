package research

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"cornwatch/internal/domain"
	"cornwatch/internal/quant"
)

func number(v float64) *float64 { return &v }

// Analyze requires one strictly ordered, single-contract daily series. Every
// point depends only on its own prefix; no centered or future-filled indicators.
func Analyze(bars []domain.Bar, allBars []domain.Bar) (MarketReport, error) {
	if err := quant.ValidateBars(bars); err != nil {
		return MarketReport{}, err
	}
	return analyze(bars, allBars)
}

// AnalyzeObserved validates prices for descriptive research only. It does not
// require or manufacture settlement and price limits that a public source may omit.
func AnalyzeObserved(bars []domain.Bar, hasOpenInterest bool) (MarketReport, error) {
	if len(bars) == 0 || len(bars) > 2000 {
		return MarketReport{}, fmt.Errorf("研究日线数量无效")
	}
	previous := ""
	for _, b := range bars {
		if b.Symbol != bars[0].Symbol || b.IsDemo || b.Timestamp.IsZero() || b.TradeDate <= previous || b.Timestamp.In(time.FixedZone("Asia/Shanghai", 8*3600)).Format("2006-01-02") != b.TradeDate {
			return MarketReport{}, fmt.Errorf("研究日线需为同一真实合约且日期严格递增")
		}
		for _, v := range []float64{b.Open, b.High, b.Low, b.Close} {
			if math.IsNaN(v) || math.IsInf(v, 0) || v <= 0 || v > 1e9 {
				return MarketReport{}, fmt.Errorf("研究价格无效")
			}
		}
		if b.Low > b.High || b.Open < b.Low || b.Open > b.High || b.Close < b.Low || b.Close > b.High || b.Volume < 0 || b.OpenInterest < 0 {
			return MarketReport{}, fmt.Errorf("研究量价范围无效")
		}
		previous = b.TradeDate
	}
	r, err := analyze(bars, nil)
	if err != nil {
		return r, err
	}
	if !hasOpenInterest {
		r.Latest.OIChange = nil
		r.Latest.OIChangePct = nil
		r.Latest.PriceOILabel = "来源存在持仓缺失，未比较增减仓"
		r.Insights[0].Body = r.Latest.PriceOILabel
		r.Warnings = append(r.Warnings, "部分日线未披露持仓，0仅为缺失占位，不用于判断资金方向。")
	}
	return r, nil
}
func analyze(bars []domain.Bar, allBars []domain.Bar) (MarketReport, error) {
	last := bars[len(bars)-1]
	for _, b := range bars {
		if b.Symbol != last.Symbol || b.IsDemo != last.IsDemo {
			return MarketReport{}, fmt.Errorf("market research requires one contract and one data mode")
		}
	}
	r := MarketReport{Symbol: last.Symbol, IsDemo: last.IsDemo, AsOf: last.Timestamp, BarCount: len(bars), Points: make([]Point, 0, len(bars)), Insights: []Insight{}, Warnings: []string{}, TermStructure: []TermPoint{}}
	var sum5, sum20, sum60, trSum, atr, gain, loss, ema12, ema26, signal, signalSum float64
	macdCount := 0
	for i, b := range bars {
		p := Point{Timestamp: b.Timestamp, TradeDate: b.TradeDate, Open: b.Open, High: b.High, Low: b.Low, Close: b.Close, Volume: b.Volume, OpenInterest: b.OpenInterest}
		sum5 += b.Close
		sum20 += b.Close
		sum60 += b.Close
		if i >= 5 {
			sum5 -= bars[i-5].Close
		}
		if i >= 20 {
			sum20 -= bars[i-20].Close
		}
		if i >= 60 {
			sum60 -= bars[i-60].Close
		}
		if i >= 4 {
			p.MA5 = number(sum5 / 5)
		}
		if i >= 19 {
			p.MA20 = number(sum20 / 20)
		}
		if i >= 59 {
			p.MA60 = number(sum60 / 60)
		}
		tr := b.High - b.Low
		if i > 0 {
			tr = math.Max(tr, math.Max(math.Abs(b.High-bars[i-1].Close), math.Abs(b.Low-bars[i-1].Close)))
		}
		if i < 14 {
			trSum += tr
		}
		if i == 13 {
			atr = trSum / 14
		} else if i > 13 {
			atr = (atr*13 + tr) / 14
		}
		if i >= 13 {
			p.ATR14 = number(atr)
		}
		if i > 0 {
			change := b.Close - bars[i-1].Close
			g, l := math.Max(change, 0), math.Max(-change, 0)
			if i <= 14 {
				gain += g
				loss += l
			}
			if i == 14 {
				gain /= 14
				loss /= 14
			} else if i > 14 {
				gain = (gain*13 + g) / 14
				loss = (loss*13 + l) / 14
			}
			if i >= 14 {
				rsi := 50.0 // Explicit convention for a completely flat series.
				if gain+loss > 0 {
					rsi = 100 * gain / (gain + loss)
				}
				p.RSI14 = number(rsi)
			}
		}
		if i < 12 {
			ema12 += b.Close
		}
		if i == 11 {
			ema12 /= 12
		} else if i > 11 {
			ema12 += (b.Close - ema12) * 2 / 13
		}
		if i < 26 {
			ema26 += b.Close
		}
		if i == 25 {
			ema26 /= 26
		} else if i > 25 {
			ema26 += (b.Close - ema26) * 2 / 27
		}
		if i >= 25 {
			diff := ema12 - ema26
			p.MACD = number(diff)
			macdCount++
			if macdCount <= 9 {
				signalSum += diff
			}
			if macdCount == 9 {
				signal = signalSum / 9
			} else if macdCount > 9 {
				signal += (diff - signal) * 2 / 10
			}
			if macdCount >= 9 {
				p.MACDSignal = number(signal)
				p.MACDHist = number(2 * (diff - signal))
			}
		}
		if i >= 20 {
			hi, lo := bars[i-20].High, bars[i-20].Low
			for j := i - 19; j < i; j++ {
				hi = math.Max(hi, bars[j].High)
				lo = math.Min(lo, bars[j].Low)
			}
			p.PriorHigh20, p.PriorLow20 = number(hi), number(lo)
		}
		r.Points = append(r.Points, p)
	}
	p := r.Points[len(r.Points)-1]
	r.Latest = Latest{Close: p.Close, ATR14: p.ATR14, RSI14: p.RSI14, MA20: p.MA20, MA60: p.MA60, PriorHigh20: p.PriorHigh20, PriorLow20: p.PriorLow20, Trend: "历史不足，等待60根日K", PriceOILabel: "历史不足，无法比较量仓"}
	if p.ATR14 != nil {
		r.Latest.ATRPct = number(*p.ATR14 / p.Close * 100)
	}
	if p.MA60 != nil {
		r.Latest.Trend = "均线交错，震荡或过渡状态"
		if p.Close > *p.MA20 && *p.MA20 > *p.MA60 {
			r.Latest.Trend = "价格与均线偏多排列（状态描述）"
		}
		if p.Close < *p.MA20 && *p.MA20 < *p.MA60 {
			r.Latest.Trend = "价格与均线偏空排列（状态描述）"
		}
	}
	if len(bars) > 1 {
		prev := bars[len(bars)-2]
		priceChange, oiChange := last.Close-prev.Close, last.OpenInterest-prev.OpenInterest
		r.Latest.ChangePct = number(priceChange / prev.Close * 100)
		r.Latest.OIChange = &oiChange
		if prev.OpenInterest > 0 {
			r.Latest.OIChangePct = number(float64(oiChange) / float64(prev.OpenInterest) * 100)
		}
		direction, oi := "价格持平", "持仓持平"
		if priceChange > 0 {
			direction = "价格上涨"
		} else if priceChange < 0 {
			direction = "价格下跌"
		}
		if oiChange > 0 {
			oi = "增仓"
		} else if oiChange < 0 {
			oi = "减仓"
		}
		r.Latest.PriceOILabel = direction + " · " + oi + "（仅现象，不能识别单边资金）"
	}
	if len(bars) > 20 {
		var vol float64
		for _, b := range bars[len(bars)-21 : len(bars)-1] {
			vol += float64(b.Volume)
		}
		if vol > 0 {
			r.Latest.VolumeRatio20 = number(float64(last.Volume) / (vol / 20))
		}
	}
	r.Insights = append(r.Insights, Insight{Title: "价格、成交量与持仓量", Body: r.Latest.PriceOILabel + "。每份未平仓合约同时有多空双方；增减仓也受套保、移仓和双方开平组合影响。应结合期限结构、现货及事件检验假设。", Tone: "neutral"})
	if p.PriorHigh20 != nil {
		body := fmt.Sprintf("前20根已完成日K区间 %.0f—%.0f 元/吨；阈值排除当前K线。收盘仍在区间内。", *p.PriorLow20, *p.PriorHigh20)
		if p.Close > *p.PriorHigh20 {
			body = fmt.Sprintf("收盘高于前20根日K最高价 %.0f 元/吨；这是历史突破状态，需后续成交量、波动和交易成本检验，不能保证延续。", *p.PriorHigh20)
		}
		if p.Close < *p.PriorLow20 {
			body = fmt.Sprintf("收盘低于前20根日K最低价 %.0f 元/吨；这是历史破位状态，需后续成交量、波动和交易成本检验，不能保证延续。", *p.PriorLow20)
		}
		r.Insights = append(r.Insights, Insight{Title: "区间与突破", Body: body, Tone: "neutral"})
	}
	if p.ATR14 != nil {
		r.Insights = append(r.Insights, Insight{Title: "波动与风险", Body: fmt.Sprintf("ATR14 为 %.2f 元/吨，占收盘价 %.2f%%。ATR描述近期真实波幅，不能覆盖极端跳空；止损距离应结合结构与账户风险预算。", *p.ATR14, *r.Latest.ATRPct), Tone: "neutral"})
	}
	if p.RSI14 != nil && (*p.RSI14 >= 70 || *p.RSI14 <= 30) {
		r.Insights = append(r.Insights, Insight{Title: "动量处于常用极值区间", Body: "RSI进入70以上或30以下只说明近期涨跌强弱不平衡；趋势中可持续较长时间，不能直接视为反转或开仓指令。", Tone: "warning"})
	}
	if len(bars) < 60 {
		r.Warnings = append(r.Warnings, "样本不足60根日K，长周期状态或部分指标为空；不以0代替未知。")
	}
	if last.IsDemo {
		r.Warnings = append(r.Warnings, "当前为合成演示数据，指标只用于检验界面和算法，不能据此判断真实市场。")
	}
	r.Warnings = append(r.Warnings, "以上均为截至所示日K结束时的描述；不是盘中实时盘口、预测概率或自动交易信号。MACD柱=2×(DIF−DEA)，不同软件初值口径可能不同。")
	r.Warnings = append(r.Warnings, "日K涨跌幅按前一收盘价比较，可能不同于行情终端按前一结算价计算的涨跌幅。时间连续性依输入记录，尚未用交易日历验证漏数。")
	r.TermStructure, r.Spread = terms(last, allBars)
	if len(r.TermStructure) < 2 {
		r.Warnings = append(r.Warnings, "同一交易日、同一数据模式的有效玉米合约不足2个，无法计算跨期价差；不会用旧价、未来价或演示数据补齐。")
	}
	if r.Spread != nil {
		r.Insights = append(r.Insights, Insight{Title: "玉米期限结构", Body: fmt.Sprintf("%s − %s = %.0f 元/吨，取同日可见合约中最近与最远月份。期限差反映储存、季节性和供需预期等综合因素；不等于无风险套利，亦不是现货基差。", r.Spread.NearSymbol, r.Spread.FarSymbol, r.Spread.NearMinusFar), Tone: "neutral"})
	}
	return r, nil
}

func terms(last domain.Bar, bars []domain.Bar) ([]TermPoint, *Spread) {
	chosen := map[string]domain.Bar{last.Symbol: last}
	conflict := map[string]bool{}
	for _, b := range bars {
		if b.Symbol == last.Symbol || b.TradeDate != last.TradeDate || b.IsDemo != last.IsDemo || b.Timestamp.After(last.Timestamp) || !strings.HasPrefix(b.Symbol, "c") {
			continue
		}
		if quant.ValidateBars([]domain.Bar{b}) != nil {
			continue
		}
		old, ok := chosen[b.Symbol]
		if !ok || b.Timestamp.After(old.Timestamp) {
			chosen[b.Symbol] = b
			conflict[b.Symbol] = false
		} else if b.Timestamp.Equal(old.Timestamp) && b != old {
			conflict[b.Symbol] = true
		}
	}
	result := make([]TermPoint, 0, len(chosen))
	for symbol, b := range chosen {
		if !conflict[symbol] {
			result = append(result, TermPoint{Symbol: symbol, TradeDate: b.TradeDate, Close: b.Close, Volume: b.Volume, OpenInterest: b.OpenInterest})
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Symbol < result[j].Symbol })
	if len(result) < 2 {
		return result, nil
	}
	near, far := result[0], result[len(result)-1]
	return result, &Spread{NearSymbol: near.Symbol, FarSymbol: far.Symbol, NearMinusFar: near.Close - far.Close, TradeDate: last.TradeDate}
}
