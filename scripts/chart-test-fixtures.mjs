// Offline chart cache only for disposable desktop test profiles. Never call
// against a user's database: the synthetic source is deliberately explicit.
import { spawnSync } from 'node:child_process';
import { mkdir, writeFile } from 'node:fs/promises';
import path from 'node:path';

export async function seedOfflineChartHistory(root, userData, symbols) {
  await mkdir(userData, { recursive: true });
  const now = new Date(), available = now.toISOString(), records = {};
  for (const symbol of new Set(symbols)) {
    const bars = Array.from({ length: 90 }, (_, i) => {
      const timestamp = new Date(Date.UTC(now.getUTCFullYear(), now.getUTCMonth(), now.getUTCDate() - 91 + i, 7));
      const close = 2200 + i;
      return { symbol, timestamp: timestamp.toISOString(), trade_date: timestamp.toISOString().slice(0, 10), open: close - 1, high: close + 2, low: close - 3, close, settlement: 0, volume: 1000, open_interest: 2000, limit_up: 0, limit_down: 0, is_demo: false, available_at: available };
    });
    records[`market_chart_v1_${symbol}_day`] = { report: { symbol, interval: 'day', bars, source: 'SYNTHETIC offline desktop fixture · 非真实行情', source_url: 'https://example.invalid/offline-test', available_at: available, has_open_interest: true, has_settlement: false, warnings: ['隔离桌面验收合成历史，仅在临时库使用，不代表实际行情。'] }, next_attempt: new Date(now.getTime() + 6 * 3600000).toISOString() };
  }
  const jsonPath = path.join(userData, 'chart-fixture.json'), goPath = path.join(userData, 'chart-fixture.go');
  await writeFile(jsonPath, JSON.stringify(records));
  await writeFile(goPath, `package main
import("encoding/json";"os";bolt "go.etcd.io/bbolt")
func main(){raw,e:=os.ReadFile(os.Args[2]);if e!=nil{panic(e)};var data map[string]json.RawMessage;if e=json.Unmarshal(raw,&data);e!=nil{panic(e)};db,e:=bolt.Open(os.Args[1],0600,nil);if e!=nil{panic(e)};defer db.Close();e=db.Update(func(tx *bolt.Tx)error{b,e:=tx.CreateBucketIfNotExists([]byte("meta"));if e!=nil{return e};for k,v:=range data{if e=b.Put([]byte(k),v);e!=nil{return e}};return nil});if e!=nil{panic(e)}}`);
  const result = spawnSync('go', ['run', goPath, path.join(userData, 'cornwatch.db'), jsonPath], { cwd: path.join(root, 'backend'), encoding: 'utf8', timeout: 60000 });
  if (result.status !== 0) throw new Error(`Offline chart fixture failed: ${result.stderr}`);
}
