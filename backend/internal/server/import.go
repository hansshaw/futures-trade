package server

import (
	"cornwatch/internal/domain"
	"cornwatch/internal/quant"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

func ParseCSV(input string) ([]domain.Bar, error) {
	r := csv.NewReader(strings.NewReader(strings.TrimPrefix(input, "\ufeff")))
	header, err := r.Read()
	if err != nil {
		return nil, errors.New("CSV 缺少表头")
	}
	columns := []string{"symbol", "timestamp", "trade_date", "open", "high", "low", "close", "settlement", "volume", "open_interest", "limit_up", "limit_down"}
	if len(header) != len(columns) {
		return nil, fmt.Errorf("CSV 需要 %d 列：%s", len(columns), strings.Join(columns, ","))
	}
	index := map[string]int{}
	for i, h := range header {
		h = strings.TrimSpace(h)
		if _, ok := index[h]; ok {
			return nil, fmt.Errorf("重复列名 %s", h)
		}
		index[h] = i
	}
	for _, name := range columns {
		if _, ok := index[name]; !ok {
			return nil, fmt.Errorf("CSV 缺少列 %s", name)
		}
	}
	result := []domain.Bar{}
	for line := 2; ; line++ {
		record, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("CSV 第 %d 行: %w", line, err)
		}
		if len(result) >= 200000 {
			return nil, errors.New("单次最多导入200000条")
		}
		get := func(name string) string { return strings.TrimSpace(record[index[name]]) }
		t, err := time.Parse(time.RFC3339Nano, get("timestamp"))
		if err != nil {
			return nil, fmt.Errorf("第%d行 timestamp 需要RFC3339时区", line)
		}
		if t.After(time.Now().Add(5 * time.Minute)) {
			return nil, fmt.Errorf("第%d行K线结束时间在未来", line)
		}
		b := domain.Bar{Symbol: strings.ToLower(get("symbol")), Timestamp: t.UTC(), TradeDate: get("trade_date")}
		fields := map[string]*float64{"open": &b.Open, "high": &b.High, "low": &b.Low, "close": &b.Close, "settlement": &b.Settlement, "limit_up": &b.LimitUp, "limit_down": &b.LimitDown}
		for key, dest := range fields {
			v, e := strconv.ParseFloat(get(key), 64)
			if e != nil {
				return nil, fmt.Errorf("第%d行 %s 不是有效数值", line, key)
			}
			*dest = v
		}
		for key, dest := range map[string]*int64{"volume": &b.Volume, "open_interest": &b.OpenInterest} {
			v, e := strconv.ParseInt(get(key), 10, 64)
			if e != nil {
				return nil, fmt.Errorf("第%d行 %s 不是整数", line, key)
			}
			*dest = v
		}
		result = append(result, b)
	}
	if len(result) == 0 {
		return nil, errors.New("CSV 没有数据行")
	}
	if err = quant.ValidateBars(result); err != nil {
		return nil, err
	}
	return result, nil
}
