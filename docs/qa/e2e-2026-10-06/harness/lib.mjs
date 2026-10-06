// Shared e2e helpers. Usage: import { open } from './lib.mjs'
import { chromium } from 'playwright'
export const BASE = 'http://localhost:8091'
export const SHOTS = '' + (process.env.SHOTS || './shots') + ''
export const CREDS = { username: process.env.QA_USER, password: process.env.QA_PASS }
/** Launch, log in, return { browser, ctx, page, errors }. errors collects console errors + page errors + failed requests (>=400). */
/** login: true reuses the shared session (do NOT log it out); login: false gives a fresh unauthenticated context. */
export async function open({ width = 1440, height = 900, colorScheme = 'light', login = true } = {}) {
  const browser = await chromium.launch()
  const fs = await import('node:fs')
  const STATE = SHOTS + '/../e2e/session.json'
  const reuse = login && fs.existsSync(STATE)
  const ctx = await browser.newContext({ viewport: { width, height }, colorScheme, ...(reuse ? { storageState: STATE } : {}) })
  const page = await ctx.newPage()
  const errors = []
  page.on('console', m => { if (m.type() === 'error') errors.push('console: ' + m.text()) })
  page.on('pageerror', e => errors.push('pageerror: ' + e.message))
  page.on('response', r => { if (r.status() >= 400 && r.url().includes('/api/')) errors.push(`http ${r.status()} ${r.request().method()} ${r.url()}`) })
  if (reuse) {
    await page.goto(BASE + '/')
    await page.waitForSelector('main', { timeout: 10000 })
    await page.waitForTimeout(500)
  } else if (login) {
    await page.goto(BASE + '/')
    await page.fill('#username, input[autocomplete=username]', CREDS.username)
    await page.fill('input[type=password]', CREDS.password)
    await page.keyboard.press('Enter')
    await page.waitForSelector('main', { timeout: 10000 })
    await page.waitForTimeout(500)
    await ctx.storageState({ path: STATE })
  }
  return { browser, ctx, page, errors }
}
export const shot = (page, name, opts = {}) => page.screenshot({ path: `${SHOTS}/${name}.png`, ...opts })
