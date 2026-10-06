// Minimal OpenAI-compatible upstream for e2e. PORT and FLAKY env select behaviour.
import http from 'node:http'
const port = +process.env.PORT, flaky = process.env.FLAKY === '1'
const models = flaky ? ['flaky-1', 'flaky-2'] : ['mock-fast', 'mock-slow', 'mock-error', 'mock-ratelimit', 'mock-embed']
let n = 0
const send = (res, code, obj) => { res.writeHead(code, { 'content-type': 'application/json' }); res.end(JSON.stringify(obj)) }
http.createServer((req, res) => {
  let body = ''
  req.on('data', c => body += c)
  req.on('end', async () => {
    const url = req.url.replace(/\?.*/, '')
    if (url.endsWith('/models')) return send(res, 200, { object: 'list', data: models.map(id => ({ id, object: 'model', owned_by: 'mock' })) })
    let j = {}; try { j = JSON.parse(body || '{}') } catch {}
    const model = j.model || models[0]
    n++
    if (url.endsWith('/embeddings')) return send(res, 200, { object: 'list', model, data: [{ object: 'embedding', index: 0, embedding: Array.from({ length: 8 }, (_, i) => i / 10) }], usage: { prompt_tokens: 5, total_tokens: 5 } })
    if (!url.endsWith('/chat/completions')) return send(res, 404, { error: { message: 'not found: ' + url } })
    if (model === 'mock-error' || (flaky && n % 2 === 0)) return send(res, 500, { error: { message: 'upstream exploded (mock)', type: 'server_error' } })
    if (model === 'mock-ratelimit') { res.setHeader('retry-after', '5'); return send(res, 429, { error: { message: 'rate limited (mock)', type: 'rate_limit' } }) }
    if (model === 'mock-slow') await new Promise(r => setTimeout(r, 2500))
    const last = (j.messages || []).slice(-1)[0]
    const text = `Mock reply from ${model} on :${port}. You said: ${typeof last?.content === 'string' ? last.content.slice(0, 80) : '[parts]'}\n\n**Bold**, a list:\n- one\n- two\n\n\`\`\`js\nconsole.log("hi")\n\`\`\``
    const usage = { prompt_tokens: 12, completion_tokens: 40, total_tokens: 52 }
    const id = 'chatcmpl-mock' + n, created = Math.floor(Date.now() / 1000)
    if (j.stream) {
      res.writeHead(200, { 'content-type': 'text/event-stream' })
      const words = text.split(/(?<= )/)
      for (const w of words) { res.write(`data: ${JSON.stringify({ id, object: 'chat.completion.chunk', created, model, choices: [{ index: 0, delta: { content: w }, finish_reason: null }] })}\n\n`); await new Promise(r => setTimeout(r, 15)) }
      res.write(`data: ${JSON.stringify({ id, object: 'chat.completion.chunk', created, model, choices: [{ index: 0, delta: {}, finish_reason: 'stop' }], usage })}\n\n`)
      res.end('data: [DONE]\n\n'); return
    }
    send(res, 200, { id, object: 'chat.completion', created, model, choices: [{ index: 0, message: { role: 'assistant', content: text }, finish_reason: 'stop' }], usage })
  })
}).listen(port, () => console.log('mock on', port, flaky ? 'flaky' : 'healthy'))
