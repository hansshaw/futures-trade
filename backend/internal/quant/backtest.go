package quant

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"time"

	"cornwatch/internal/domain"
)

const contractMultiplier = 10.0

type Config struct {
	Symbol         string  `json:"symbol"`
	Demo           bool    `json:"demo"`
	FastWindow     int     `json:"fast_window"`
	SlowWindow     int     `json:"slow_window"`
	InitialCapital float64 `json:"initial_capital"`
	Lots           int     `json:"lots"`
	FeePerLot      float64 `json:"fee_per_lot"`
	SlippageTicks  int     `json:"slippage_ticks"`
	MarginRate     float64 `json:"margin_rate"`
	MaxDrawdownPct float64 `json:"max_drawdown_pct"`
}

type Metrics struct {
	InitialCapital float64  `json:"initial_capital"`
	FinalEquity    float64  `json:"final_equity"`
	TotalReturnPct float64  `json:"total_return_pct"`
	MaxDrawdownPct float64  `json:"max_drawdown_pct"`
	TotalFees      float64  `json:"total_fees"`
	TotalSlippage  float64  `json:"total_slippage"`
	TradeCount     int      `json:"trade_count"` // Completed round trips, not individual fills.
	WinRatePct     float64  `json:"win_rate_pct"`
	SharpeRatio    *float64 `json:"sharpe_ratio,omitempty"`
	OpenPosition   int      `json:"open_position"` // Signed lots still open at the end.
	UnrealizedPnL  float64  `json:"unrealized_pnl"`
}

type EquityPoint struct {
	Timestamp   time.Time `json:"timestamp"`
	Equity      float64   `json:"equity"`
	DrawdownPct float64   `json:"drawdown_pct"`
	Position    int       `json:"position"`
}

type Trade struct {
	Timestamp time.Time `json:"timestamp"`
	Symbol    string    `json:"symbol"`
	Side      string    `json:"side"`
	Offset    string    `json:"offset"`
	Price     float64   `json:"price"`
	Lots      int       `json:"lots"`
	Fee       float64   `json:"fee"`
	Slippage  float64   `json:"slippage"` // Separate cash charge; never counted twice.
	Reason    string    `json:"reason"`
	PnL       float64   `json:"pnl"` // Cash change at this fill, including this side's costs.
}

type Result struct {
	ID              string        `json:"id"`
	CreatedAt       time.Time     `json:"created_at"`
	Symbol          string        `json:"symbol"`
	IsDemo          bool          `json:"is_demo"`
	Config          Config        `json:"config"`
	Metrics         Metrics       `json:"metrics"`
	EquityCurve     []EquityPoint `json:"equity_curve"`
	Trades          []Trade       `json:"trades"`
	Warnings        []string      `json:"warnings"`
	InputSHA256     string        `json:"input_sha256"`
	EngineVersion   string        `json:"engine_version"`
	BarCount        int           `json:"bar_count"`
	EvaluationStart int           `json:"evaluation_start"` // Earlier bars are indicator warmup only.
}

func (c Config) validate() error {
	if maturity(c.Symbol) == 0 {
		return fmt.Errorf("only a single corn contract cYYMM is supported")
	}
	if c.FastWindow < 1 || c.SlowWindow <= c.FastWindow || c.SlowWindow > 5000 {
		return fmt.Errorf("windows must satisfy 1 <= fast_window < slow_window <= 5000")
	}
	if !finite(c.InitialCapital) || c.InitialCapital <= 0 || c.InitialCapital > 1e12 {
		return fmt.Errorf("initial_capital must be in (0, 1e12]")
	}
	if c.Lots < 1 || c.Lots > 10000 {
		return fmt.Errorf("lots must be in [1, 10000]")
	}
	if !finite(c.FeePerLot) || c.FeePerLot < 0 || c.FeePerLot > 1e6 {
		return fmt.Errorf("fee_per_lot must be in [0, 1e6]")
	}
	if c.SlippageTicks < 0 || c.SlippageTicks > 10000 {
		return fmt.Errorf("slippage_ticks must be in [0, 10000]")
	}
	if !finite(c.MarginRate) || c.MarginRate <= 0 || c.MarginRate > 1 {
		return fmt.Errorf("margin_rate must be in (0, 1]")
	}
	if !finite(c.MaxDrawdownPct) || c.MaxDrawdownPct <= 0 || c.MaxDrawdownPct > 100 {
		return fmt.Errorf("max_drawdown_pct must be in (0, 100]")
	}
	return nil
}

// Backtest runs a close-derived SMA trend: fast > slow targets long, fast <
// slow targets short, equality targets flat. Orders execute only at the next
// bar's open. Slippage is a tick-quantized per-side cash cost, separate from the
// observed open/close execution price. This avoids inventing prices outside OHLC.
// A scheduled liquidation is attempted at the final study day's close.
func Backtest(c Config, bars []domain.Bar) (Result, error) {
	return BacktestFrom(c, bars, 0)
}

// BacktestFrom starts with a fresh account at evaluationStart. Preceding bars
// only warm the SMA state; they never trade, incur costs, or affect risk/equity.
// The signal from the preceding close may execute at the first evaluated open.
func BacktestFrom(c Config, bars []domain.Bar, evaluationStart int) (Result, error) {
	if err := c.validate(); err != nil {
		return Result{}, err
	}
	if err := ValidateBars(bars); err != nil {
		return Result{}, err
	}
	if len(bars) <= c.SlowWindow {
		return Result{}, fmt.Errorf("need more than slow_window bars to execute a next-bar signal")
	}
	if evaluationStart < 0 || evaluationStart >= len(bars) {
		return Result{}, fmt.Errorf("evaluation_start must select a bar within the input")
	}
	for _, b := range bars {
		if b.Symbol != c.Symbol {
			return Result{}, fmt.Errorf("backtest cannot combine contracts")
		}
		if b.IsDemo != c.Demo {
			return Result{}, fmt.Errorf("demo parameter must match every input bar")
		}
	}
	idBytes := make([]byte, 16)
	if _, err := rand.Read(idBytes); err != nil {
		return Result{}, fmt.Errorf("create backtest id: %w", err)
	}
	r := Result{ID: hex.EncodeToString(idBytes), CreatedAt: time.Now().UTC(), Symbol: c.Symbol, IsDemo: c.Demo, Config: c, Trades: []Trade{}, EquityCurve: make([]EquityPoint, 0, len(bars)-evaluationStart), Warnings: []string{}, EvaluationStart: evaluationStart}
	inputJSON, err := json.Marshal(bars)
	if err != nil {
		return Result{}, fmt.Errorf("fingerprint inputs: %w", err)
	}
	digest := sha256.Sum256(inputJSON)
	r.InputSHA256, r.EngineVersion, r.BarCount = hex.EncodeToString(digest[:]), "sma-daily-v2", len(bars)-evaluationStart
	warned := map[string]bool{}
	warn := func(s string) {
		if !warned[s] {
			warned[s] = true
			r.Warnings = append(r.Warnings, s)
		}
	}
	warn("研究模型：收盘均线趋势在下一交易日开盘成交；成交价为观测价，滑点按每边整数 tick 折算现金成本；手续费和滑点均已扣除。")
	warn("日线无法重建开盘深度、盘中风险、排队成交或夜盘路径；成交时刻仅标注为所属日K结束时间，reason 标明开盘或终止日收盘。")
	warn("日末成交量仅作为事后可成交性过滤及成交量上限，不是开盘时已知信息；信号使用上一日收盘，但撮合仍是日线研究假设，不能直接用于实盘。")
	warn("净值按收盘价估值；settlement 仅作输入校验，未模拟结算价逐日结算、结算资金划转或盘中追加保证金，风控仅在开盘与收盘检查。")
	warn("保证金率和费用为固定研究参数；实际交易所及经纪商可调整，回测不含资金利息。未计算 Sharpe 比率。")
	if evaluationStart > 0 {
		warn("评估前历史仅预热均线，未成交或计费；评估起点使用独立初始资金和空仓，允许前一收盘已确定的信号在评估首日开盘执行。")
	}
	if c.Demo {
		warn("输入为合成演示数据；结果仅用于验证功能，不能作为策略收益证据。")
	}
	for _, b := range bars {
		if b.LimitUp == 0 {
			warn("部分日K缺少涨跌停价，无法完整判断这些日期的涨跌停成交限制。")
		}
	}
	cash, peak, maxDD := c.InitialCapital, c.InitialCapital, 0.0
	position, target, wins := 0, 0, 0
	entry, entryCosts := 0.0, 0.0
	fastSum, slowSum := 0.0, 0.0
	halted := false
	fee := float64(c.Lots) * c.FeePerLot
	slip := float64(c.SlippageTicks*c.Lots) * contractMultiplier
	equityAt := func(price float64) float64 { return cash + float64(position*c.Lots)*contractMultiplier*(price-entry) }
	drawdown := func(equity float64) float64 { return math.Max(0, (peak-equity)/peak*100) }
	updateSignal := func(i int, b domain.Bar) {
		fastSum += b.Close
		slowSum += b.Close
		if i >= c.FastWindow {
			fastSum -= bars[i-c.FastWindow].Close
		}
		if i >= c.SlowWindow {
			slowSum -= bars[i-c.SlowWindow].Close
		}
		if !halted && i+1 >= c.SlowWindow {
			fast, slow := fastSum/float64(c.FastWindow), slowSum/float64(c.SlowWindow)
			target = 0
			if fast > slow {
				target = 1
			} else if fast < slow {
				target = -1
			}
		}
	}
	for i, b := range bars {
		if i < evaluationStart {
			updateSignal(i, b)
			continue
		}
		remaining := b.Volume
		// Price and side restrictions are known at the proposed execution price.
		// Zero daily volume conservatively rules out any execution on that day.
		canFill := func(side string, price float64) bool {
			if remaining < int64(c.Lots) {
				warn("存在无成交量或日成交量不足的委托，已拒绝成交。")
				return false
			}
			if b.LimitUp > 0 && ((side == "buy" && price >= b.LimitUp) || (side == "sell" && price <= b.LimitDown)) {
				warn("存在买入涨停或卖出跌停的委托，按无法成交处理；日线无法证明排队成交。")
				return false
			}
			return true
		}
		fill := func(next int, price float64, reason string) bool {
			if position == next {
				return true
			}
			if position != 0 {
				side := "sell"
				if position < 0 {
					side = "buy"
				}
				if !canFill(side, price) {
					return false
				}
				gross := float64(position*c.Lots) * contractMultiplier * (price - entry)
				cash += gross - fee - slip
				r.Trades = append(r.Trades, Trade{Timestamp: b.Timestamp, Symbol: c.Symbol, Side: side, Offset: "close", Price: price, Lots: c.Lots, Fee: fee, Slippage: slip, Reason: reason, PnL: gross - fee - slip})
				r.Metrics.TotalFees += fee
				r.Metrics.TotalSlippage += slip
				r.Metrics.TradeCount++
				if gross-entryCosts-fee-slip > 1e-8 {
					wins++
				}
				remaining -= int64(c.Lots)
				position, entry, entryCosts = 0, 0, 0
			}
			if next != 0 {
				if cash <= 0 || drawdown(cash-fee-slip) >= c.MaxDrawdownPct {
					halted = true
					target = 0
					warn("平仓或预期开仓费用触发回撤风控，已停止新增仓位。")
					return false
				}
				side := "buy"
				if next < 0 {
					side = "sell"
				}
				if !canFill(side, price) {
					return false
				}
				margin := price * contractMultiplier * float64(c.Lots) * c.MarginRate
				if cash-fee-slip < margin {
					warn("存在扣除本次手续费和滑点后资金不足以覆盖开仓保证金的信号，已拒绝开仓。")
					return false
				}
				cash -= fee + slip
				position, entry, entryCosts = next, price, fee+slip
				r.Trades = append(r.Trades, Trade{Timestamp: b.Timestamp, Symbol: c.Symbol, Side: side, Offset: "open", Price: price, Lots: c.Lots, Fee: fee, Slippage: slip, Reason: reason, PnL: -fee - slip})
				r.Metrics.TotalFees += fee
				r.Metrics.TotalSlippage += slip
				remaining -= int64(c.Lots)
			}
			return true
		}
		risk := func(price float64) {
			if halted {
				return
			}
			eq := equityAt(price)
			margin := math.Abs(float64(position)) * float64(c.Lots) * price * contractMultiplier * c.MarginRate
			if eq <= 0 || drawdown(eq) >= c.MaxDrawdownPct || (position != 0 && eq < margin) {
				halted = true
				target = 0
				warn("触发权益、保证金或最大回撤风控：停止新增仓位，并在下一个可成交时点尝试平仓；跳空和涨跌停可能使损失超过阈值。")
			}
		}
		// Opening gaps are observable before sending that day's orders.
		risk(b.Open)
		if position != target {
			reason := "previous_close_sma_at_next_open"
			if halted {
				reason = "risk_liquidation_at_open"
			}
			fill(target, b.Open, reason)
		}
		if i == len(bars)-1 && position != 0 {
			fill(0, b.Close, "scheduled_end_of_test_close")
		}
		eq := equityAt(b.Close)
		if eq > peak {
			peak = eq
		}
		dd := drawdown(eq)
		if dd > maxDD {
			maxDD = dd
		}
		r.EquityCurve = append(r.EquityCurve, EquityPoint{Timestamp: b.Timestamp, Equity: eq, DrawdownPct: dd, Position: position * c.Lots})
		risk(b.Close)
		updateSignal(i, b)
	}
	last := bars[len(bars)-1]
	r.Metrics.InitialCapital = c.InitialCapital
	r.Metrics.FinalEquity = equityAt(last.Close)
	r.Metrics.TotalReturnPct = (r.Metrics.FinalEquity/c.InitialCapital - 1) * 100
	r.Metrics.MaxDrawdownPct = maxDD
	r.Metrics.OpenPosition = position * c.Lots
	r.Metrics.UnrealizedPnL = float64(position*c.Lots) * contractMultiplier * (last.Close - entry)
	if r.Metrics.TradeCount > 0 {
		r.Metrics.WinRatePct = float64(wins) / float64(r.Metrics.TradeCount) * 100
	} else {
		warn("没有已完成的往返交易，胜率字段为0占位，不代表已估计胜率。")
	}
	if position != 0 {
		warn("末尾平仓未成交，仍有未平持仓：final_equity 含按最后收盘价盯市的浮动盈亏，未扣除尚未发生的退出费用和滑点；不是完全平仓收益。")
	}
	return r, nil
}
