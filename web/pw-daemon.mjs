// 持久头显浏览器守护:headless:false,开 CDP 端口供 pw-act.mjs 逐步连入操作。
// 放 web/ 下以复用 node_modules 的 @playwright/test。运行:cd web && node pw-daemon.mjs
import { chromium } from '@playwright/test'

const browser = await chromium.launch({
  headless: false,
  slowMo: 250,
  args: ['--remote-debugging-port=9333', '--window-size=1480,940'],
})
const ctx = await browser.newContext({ viewport: { width: 1440, height: 880 } })
await ctx.newPage()
console.log('DAEMON_READY cdp=http://localhost:9333')

// 保活,直到被杀。
await new Promise(() => {})
