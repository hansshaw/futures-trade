import { spawn, spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { setTimeout } from 'node:timers/promises';
import path from 'node:path';
import electron from 'electron';
const root = fileURLToPath(new URL('../../', import.meta.url));
const build = spawnSync(process.execPath, [path.join(root, 'desktop/scripts/build-backend.mjs')], { stdio: 'inherit', cwd: root });
if (build.status !== 0) process.exit(build.status ?? 1);
const vite = spawn(process.execPath, [path.join(root, 'node_modules/vite/bin/vite.js'), '--config', 'desktop/vite.config.ts', '--host', '127.0.0.1'], { cwd: root, stdio: 'inherit' });
let app;
const cleanup = () => { app?.kill(); vite.kill(); };
process.once('SIGINT', () => { cleanup(); process.exit(0); });
process.once('SIGTERM', () => { cleanup(); process.exit(0); });
vite.once('exit', code => { if (code) { cleanup(); process.exit(code); } });
let ready = false;
for (let i = 0; i < 100; i++) {
  try { const response = await fetch('http://127.0.0.1:5173'); if (response.ok) { ready = true; break; } } catch { /* Wait for Vite. */ }
  await setTimeout(100);
}
if (!ready) { console.error('Vite 开发服务器启动超时'); cleanup(); process.exit(1); }
const env = { ...process.env, CORNWATCH_DEV: '1' };
delete env.ELECTRON_RUN_AS_NODE;
app = spawn(electron, ['.'], { cwd: root, env, stdio: 'inherit' });
app.once('exit', code => { vite.kill(); process.exit(code ?? 0); });
