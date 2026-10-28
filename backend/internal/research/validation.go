package research

import (
	"fmt"

	"cornwatch/internal/domain"
	"cornwatch/internal/quant"
)

// ValidateStrategy is a fixed-parameter temporal holdout study, not parameter
// fitting, walk-forward optimization, or evidence of statistical significance.
func ValidateStrategy(c quant.Config, bars []domain.Bar) (ValidationReport, error) {
	if len(bars) < 80 {
		return ValidationReport{}, fmt.Errorf("时间切分研究至少需要80根日K（当前%d根）", len(bars))
	}
	split := len(bars) * 7 / 10
	if split <= c.SlowWindow {
		return ValidationReport{}, fmt.Errorf("训练段需要多于慢线窗口的日K，请增加历史区间或缩短预先选定的窗口")
	}
	if c.FeePerLot > 5e5 || c.SlippageTicks > 5000 {
		return ValidationReport{}, fmt.Errorf("双倍成本研究要求fee_per_lot <= 500000、slippage_ticks <= 5000")
	}
	// The baseline validates the full sequence/mode before any other evaluation.
	test, err := quant.BacktestFrom(c, bars, split)
	if err != nil {
		return ValidationReport{}, err
	}
	train, err := quant.Backtest(c, bars[:split])
	if err != nil {
		return ValidationReport{}, err
	}
	stressConfig := c
	stressConfig.FeePerLot *= 2
	stressConfig.SlippageTicks *= 2
	stress, err := quant.BacktestFrom(stressConfig, bars, split)
	if err != nil {
		return ValidationReport{}, err
	}
	r := ValidationReport{Symbol: c.Symbol, IsDemo: c.Demo, Config: c, InputSHA256: test.InputSHA256, EngineVersion: test.EngineVersion, SplitDate: bars[split].TradeDate, TrainBars: split, TestBars: len(bars) - split, Segments: []Segment{
		{Name: "训练段（固定参数）", Start: bars[0].TradeDate, End: bars[split-1].TradeDate, BarCount: split, Metrics: train.Metrics},
		{Name: "测试段（样本外）", Start: bars[split].TradeDate, End: bars[len(bars)-1].TradeDate, BarCount: len(bars) - split, Metrics: test.Metrics},
	}, CostScenarios: []CostScenario{
		{Name: "测试段 · 基准成本", FeePerLot: c.FeePerLot, SlippageTicks: c.SlippageTicks, Metrics: test.Metrics},
		{Name: "测试段 · 双倍费用与滑点", FeePerLot: stressConfig.FeePerLot, SlippageTicks: stressConfig.SlippageTicks, Metrics: stress.Metrics},
	}, Warnings: []string{
		"按时间前70%/后30%切分；所有参数由调用者预先指定，程序没有搜索或按测试结果选择参数。训练段只是固定参数对照段，不代表模型已拟合。",
		"训练与测试使用各自独立初始权益和空仓，各自在末日尝试平仓；测试首日可使用此前训练历史预热的均线信号，不继承训练持仓、盈亏或风控停机状态。",
		"双倍成本场景只改变测试段每边手续费和滑点；成本也会改变风险停机与成交次数，因此压力收益不保证单调下降，需同时检查交易数、回撤和未平仓。",
		"单次留出法不能消除多重试验和过拟合；查看测试结果后再调参会使该测试集失去独立性。应冻结版本、保留新数据前向验证；尚未实现自动walk-forward或置信区间。",
	}}
	seen := map[string]bool{}
	for _, pair := range []struct {
		name   string
		result quant.Result
	}{{"训练段", train}, {"测试段", test}, {"双倍成本测试段", stress}} {
		for _, w := range pair.result.Warnings {
			// Shared assumptions are emitted once; unresolved positions and halted
			// accounts retain the scenario name so their scope is not concealed.
			if !seen[w] {
				r.Warnings = append(r.Warnings, w)
				seen[w] = true
			}
		}
		if pair.result.Metrics.OpenPosition != 0 {
			r.Warnings = append(r.Warnings, fmt.Sprintf("%s期末仍有%d手未平仓，收益包含未实现盈亏。", pair.name, pair.result.Metrics.OpenPosition))
		}
		if pair.result.Metrics.TradeCount < 10 {
			r.Warnings = append(r.Warnings, fmt.Sprintf("%s仅%d次已平往返交易，样本很少，不能据此推断稳定胜率或收益。", pair.name, pair.result.Metrics.TradeCount))
		}
	}
	return r, nil
}
