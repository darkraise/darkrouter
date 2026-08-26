import { useRef, useState } from "react"
import { Link } from "@tanstack/react-router"
import { Button } from "darkraise-ui/components/button"
import { Card } from "darkraise-ui/components/card"
import { Input } from "darkraise-ui/components/input"
import { stream, type StreamStart } from "../../lib/api"

/** Reads the assistant text out of whatever complete SSE frames have arrived. */
function drainSSE(buffer: string): { text: string; rest: string } {
  let text = ""
  let rest = buffer
  for (;;) {
    const i = rest.indexOf("\n\n")
    if (i < 0) break
    const frame = rest.slice(0, i)
    rest = rest.slice(i + 2)
    for (const line of frame.split("\n")) {
      if (!line.startsWith("data: ")) continue
      const payload = line.slice(6)
      if (payload === "[DONE]") continue
      try {
        const obj = JSON.parse(payload) as {
          choices?: { delta?: { content?: string } }[]
        }
        const delta = obj?.choices?.[0]?.delta?.content
        if (typeof delta === "string") text += delta
      } catch {
        // A frame that is not JSON is a provider quirk, not a client error.
        // Skipping it beats aborting a stream that is otherwise fine.
      }
    }
  }
  return { text, rest }
}

export function PlaygroundScreen() {
  const [model, setModel] = useState("")
  const [prompt, setPrompt] = useState("")
  const [output, setOutput] = useState("")
  const [requestID, setRequestID] = useState("")
  const [error, setError] = useState("")
  const [busy, setBusy] = useState(false)
  const buffer = useRef("")

  async function send() {
    setBusy(true)
    setOutput("")
    setRequestID("")
    setError("")
    buffer.current = ""
    try {
      for await (const chunk of stream(
        "/api/playground",
        { model, prompt },
        // The id arrives with the headers, before the body this is rendering.
        (s: StreamStart) => setRequestID(s.requestId),
      )) {
        buffer.current += chunk
        const { text, rest } = drainSSE(buffer.current)
        buffer.current = rest
        if (text) setOutput((o) => o + text)
      }
    } catch (err) {
      setError((err as Error).message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="flex flex-col gap-4 p-6">
      <Input
        placeholder="alias or provider/model"
        value={model}
        onChange={(e) => setModel(e.target.value)}
      />
      <Input
        placeholder="Prompt"
        value={prompt}
        onChange={(e) => setPrompt(e.target.value)}
      />
      <div>
        <Button onClick={() => void send()} disabled={busy || !model || !prompt}>
          {busy ? "Streaming…" : "Send"}
        </Button>
      </div>
      {error ? <p className="text-destructive text-sm">{error}</p> : null}
      <Card className="min-h-40 p-4 font-mono text-sm whitespace-pre-wrap">{output}</Card>
      {requestID ? (
        // Spec §6: "follow a link to the trace it produced". Verifying a
        // credential means seeing which provider actually served.
        <Link to="/requests/$id" params={{ id: requestID }} className="text-sm underline">
          View the trace for this request
        </Link>
      ) : null}
    </div>
  )
}
