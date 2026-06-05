// 连入 pw-daemon 的头显浏览器,按 JSON 步骤逐步操作 + 截图。
// 用法:cd web && node pw-act.mjs /tmp/pw-real/steps.json
import { chromium } from '@playwright/test'
import { readFileSync, mkdirSync } from 'node:fs'

const SHOTS = '/tmp/pw-real/shots'
mkdirSync(SHOTS, { recursive: true })

const stepsFile = process.argv[2]
const steps = JSON.parse(readFileSync(stepsFile, 'utf8'))

const browser = await chromium.connectOverCDP('http://localhost:9333')
const ctx = browser.contexts()[0]
let page = ctx.pages()[0] || (await ctx.newPage())

function loc(s) {
  if (s.sel) return s.nth != null ? page.locator(s.sel).nth(s.nth) : page.locator(s.sel)
  if (s.role) return page.getByRole(s.role, s.name ? { name: s.name } : {})
  if (s.text) return s.exact ? page.getByText(s.text, { exact: true }) : page.getByText(s.text).first()
  if (s.label) return page.getByLabel(s.label)
  if (s.placeholder) return page.getByPlaceholder(s.placeholder)
  throw new Error('no locator in ' + JSON.stringify(s))
}

for (let i = 0; i < steps.length; i++) {
  const s = steps[i]
  const tag = `[${i + 1}/${steps.length}] ${s.do}`
  try {
    if (s.do === 'goto') {
      await page.goto(s.url, { waitUntil: 'domcontentloaded' })
    } else if (s.do === 'click') {
      await loc(s).click({ timeout: s.timeout ?? 12000 })
    } else if (s.do === 'fill') {
      await loc(s).fill(String(s.value), { timeout: s.timeout ?? 12000 })
    } else if (s.do === 'press') {
      await page.keyboard.press(s.key)
    } else if (s.do === 'wait') {
      if (s.ms) await page.waitForTimeout(s.ms)
      else if (s.url) await page.waitForURL(s.url, { timeout: s.timeout ?? 15000 })
      else if (s.sel) await page.locator(s.sel).first().waitFor({ timeout: s.timeout ?? 15000 })
      else if (s.text) await page.getByText(s.text).first().waitFor({ timeout: s.timeout ?? 15000 })
    } else if (s.do === 'eval') {
      const r = await page.evaluate(s.js)
      console.log(tag, '=>', JSON.stringify(r))
    } else if (s.do === 'text') {
      const t = await page.locator(s.sel || 'body').first().innerText()
      console.log(tag, '\n' + t.slice(0, s.limit ?? 1500))
    } else if (s.do === 'shot') {
      const p = `${SHOTS}/${s.name}.png`
      await page.screenshot({ path: p, fullPage: !!s.full })
      console.log(tag, '=>', p)
      continue
    } else {
      throw new Error('unknown do ' + s.do)
    }
    console.log(tag, 'ok')
  } catch (e) {
    console.log(tag, 'ERR', e.message.split('\n')[0])
    const p = `${SHOTS}/err-${i + 1}.png`
    try { await page.screenshot({ path: p }) } catch {}
    console.log('  screenshot:', p)
    if (!s.soft) break
  }
}

// 末尾常规截图 + 当前 URL
try {
  console.log('URL:', page.url())
  await page.screenshot({ path: `${SHOTS}/_last.png` })
} catch {}
await browser.close() // 仅断开 CDP,不关守护浏览器
