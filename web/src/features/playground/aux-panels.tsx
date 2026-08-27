import { useEffect, useRef, useState, type RefObject } from "react"
import { Link } from "@tanstack/react-router"
import { Button } from "darkraise-ui/components/button"
import { Card } from "darkraise-ui/components/card"
import { Input } from "darkraise-ui/components/input"
import { Textarea } from "darkraise-ui/components/textarea"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "darkraise-ui/components/select"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "darkraise-ui/components/tabs"
import { ApiError, getCsrfToken, loggedOut } from "../../lib/api"
import type { AuxBody, AuxSurface, CountResult } from "../../lib/api-types"

export const AUX_SURFACES = [
  { surface: "embeddings", label: "Embeddings", needsFile: false },
  { surface: "rerank", label: "Rerank", needsFile: false },
  { surface: "moderations", label: "Moderation", needsFile: false },
  { surface: "images", label: "Images", needsFile: false },
  { surface: "speech", label: "Speech", needsFile: false },
  { surface: "transcriptions", label: "Transcription", needsFile: true },
] as const

const NUMERIC = new Set(["dimensions", "n", "top_n"])

export function auxBodyFor(surface: AuxSurface, form: Record<string, string>): AuxBody {
  const body: Record<string, unknown> = {}
  const out: AuxBody = { surface, model: form.model, body }
  for (const [k, v] of Object.entries(form)) {
    if (k === "model" || v === "") continue
    if (k === "file_b64") {
      out.file_b64 = v
      continue
    }
    if (k === "filename") {
      out.filename = v
      continue
    }
    body[k] = NUMERIC.has(k) ? Number(v) : v
  }
  return out
}

export function vectorPreview(embedding: number[], n: number): string {
  const head = embedding.slice(0, n).map((v) => v.toFixed(3)).join(", ")
  // The length is the fact an operator is checking; a 1536-component vector
  // printed whole is unreadable.
  const ellipsis = embedding.length > n ? ", …" : ""
  return `[${head}${ellipsis}] (${embedding.length} components)`
}

export function readCount(res: Response, body: unknown): CountResult {
  const shape = (body ?? {}) as Record<string, unknown>
  const tokens =
    typeof shape.input_tokens === "number"
      ? shape.input_tokens
      : typeof shape.totalTokens === "number"
        ? shape.totalTokens
        : 0
  return {
    tokens,
    // Set by exec.HandleCount when no candidate spoke the native counting
    // dialect. The body cannot carry it, so the header is the only signal.
    estimated: res.headers.get("X-Darkrouter-Estimated") === "true",
  }
}

/**
 * Distinguishes a dead session's 401 from a legitimate rejection shaped like
 * one, and throws for both. The aux and count calls read response headers
 * (the request id, the estimate marker) that `api.post` does not expose, so
 * they fetch directly and land on the same ambiguity `stream()` already
 * resolves for chat: every admin-issued rejection — a dead session included
 * — shapes its body as {"error": "<string>"}; only a body that reached a
 * dialect writer nests an object there instead, which means the session
 * held and the executor itself is calling the request bad.
 */
async function throwForError(res: Response): Promise<never> {
  let message = res.statusText
  let sessionDead = res.status === 401
  try {
    const parsed = (await res.json()) as { error?: unknown }
    if (typeof parsed.error === "string") {
      message = parsed.error
    } else if (parsed.error && typeof parsed.error === "object") {
      sessionDead = false
      const nested = parsed.error as { message?: string }
      if (nested.message) message = nested.message
    }
  } catch {
    // A non-JSON error body means something upstream of the API answered.
    // The status line is all there is to report.
  }
  if (sessionDead) loggedOut()
  throw new ApiError(res.status, message)
}

/**
 * The one fetch call this file makes. Callers need the raw `Response` for
 * its headers and, for speech, its bytes — exactly what `api.post` cannot
 * give them, which is why this exists instead of reusing it.
 */
async function postRaw(path: string, body: unknown): Promise<Response> {
  const res = await fetch(path, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      "X-CSRF-Token": getCsrfToken(),
      "Sec-Fetch-Site": "same-origin",
    },
    body: JSON.stringify(body),
    credentials: "same-origin",
  })
  if (!res.ok) await throwForError(res)
  return res
}

function readFileAsBase64(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => {
      const result = reader.result as string
      // A data: URL prefixes the payload with "data:<mime>;base64,"; the
      // wire format wants the base64 payload alone.
      const comma = result.indexOf(",")
      resolve(comma >= 0 ? result.slice(comma + 1) : result)
    }
    reader.onerror = () => reject(reader.error ?? new Error("failed to read file"))
    reader.readAsDataURL(file)
  })
}

type FieldSpec = { key: string; label: string; type?: "number"; multiline?: boolean }

type FormSurface = Exclude<AuxSurface, "transcriptions">

const SURFACE_FIELDS: Record<FormSurface, FieldSpec[]> = {
  embeddings: [
    { key: "input", label: "Input", multiline: true },
    { key: "dimensions", label: "Dimensions", type: "number" },
  ],
  rerank: [
    { key: "query", label: "Query" },
    { key: "documents", label: "Documents, one per line", multiline: true },
  ],
  moderations: [{ key: "input", label: "Input", multiline: true }],
  images: [
    { key: "prompt", label: "Prompt", multiline: true },
    { key: "n", label: "n", type: "number" },
    { key: "size", label: "Size, e.g. 1024x1024" },
  ],
  speech: [
    { key: "input", label: "Input", multiline: true },
    { key: "voice", label: "Voice" },
  ],
}

type AuxResult =
  | { kind: "embedding"; requestId: string; preview: string }
  | { kind: "image"; requestId: string; urls: string[] }
  | { kind: "audio"; requestId: string; url: string }
  | { kind: "json"; requestId: string; text: string }

async function readAuxResult(
  surface: AuxSurface,
  res: Response,
  requestId: string,
  audioUrls: RefObject<Partial<Record<AuxSurface, string>>>,
): Promise<AuxResult> {
  if (surface === "speech") {
    // A stale blob URL for this surface is never navigated to again once a
    // new one replaces it in state, so it is revoked here rather than left
    // for the browser to notice on its own.
    const prior = audioUrls.current[surface]
    if (prior) URL.revokeObjectURL(prior)
    const blob = await res.blob()
    const url = URL.createObjectURL(blob)
    audioUrls.current[surface] = url
    return { kind: "audio", requestId, url }
  }
  const json = (await res.json()) as Record<string, unknown>
  if (surface === "embeddings") {
    const data = json.data as { embedding?: number[] }[] | undefined
    return { kind: "embedding", requestId, preview: vectorPreview(data?.[0]?.embedding ?? [], 8) }
  }
  if (surface === "images") {
    const data = (json.data as { b64_json?: string; url?: string }[] | undefined) ?? []
    const urls = data.map((d) => (d.b64_json ? `data:image/png;base64,${d.b64_json}` : (d.url ?? "")))
    return { kind: "image", requestId, urls }
  }
  return { kind: "json", requestId, text: JSON.stringify(json, null, 2) }
}

function AuxResultView({ result }: { result: AuxResult }) {
  return (
    <Card className="flex flex-col gap-2 p-3 text-sm">
      {result.kind === "embedding" ? <p className="font-mono">{result.preview}</p> : null}
      {result.kind === "image" ? (
        <div className="flex flex-wrap gap-2">
          {result.urls.map((url, i) => (
            <img key={i} src={url} alt="" className="max-h-48 rounded" />
          ))}
        </div>
      ) : null}
      {result.kind === "audio" ? <audio controls src={result.url} /> : null}
      {result.kind === "json" ? <pre className="font-mono whitespace-pre-wrap">{result.text}</pre> : null}
      <Link to="/requests/$id" params={{ id: result.requestId }} className="underline">
        View the trace for this request
      </Link>
    </Card>
  )
}

function AuxSurfaceForm({
  surface,
  needsFile,
  form,
  busy,
  error,
  result,
  onField,
  onFile,
  onRun,
}: {
  surface: AuxSurface
  needsFile: boolean
  form: Record<string, string>
  busy: boolean
  error: string
  result: AuxResult | undefined
  onField: (key: string, value: string) => void
  onFile: (file: File) => void
  onRun: () => void
}) {
  return (
    <div className="flex flex-col gap-3">
      <Input
        placeholder="model"
        value={form.model ?? ""}
        onChange={(e) => onField("model", e.target.value)}
      />
      {needsFile ? (
        <div className="flex flex-col gap-2">
          <input
            type="file"
            onChange={(e) => {
              const file = e.target.files?.[0]
              if (file) onFile(file)
            }}
          />
          {form.filename ? (
            <p className="text-sm text-[hsl(var(--muted-foreground))]">{form.filename}</p>
          ) : null}
        </div>
      ) : (
        SURFACE_FIELDS[surface as FormSurface].map((f) =>
          f.multiline ? (
            <Textarea
              key={f.key}
              placeholder={f.label}
              value={form[f.key] ?? ""}
              onChange={(e) => onField(f.key, e.target.value)}
            />
          ) : (
            <Input
              key={f.key}
              type={f.type ?? "text"}
              placeholder={f.label}
              value={form[f.key] ?? ""}
              onChange={(e) => onField(f.key, e.target.value)}
            />
          ),
        )
      )}
      <Button onClick={onRun} disabled={busy}>
        {busy ? "Running…" : "Run"}
      </Button>
      {error ? <p className="text-destructive text-sm">{error}</p> : null}
      {result ? <AuxResultView result={result} /> : null}
    </div>
  )
}

/** Every aux surface's form, run and result, in one Tabs strip. Counting is
 *  a separate screen: it hits a different endpoint with a narrower shape
 *  (dialect, model, prompt — no file, no surface-specific body) and gains
 *  nothing from sharing a tab with a file upload or an image prompt. */
export function AuxPanels() {
  const [forms, setForms] = useState<Record<AuxSurface, Record<string, string>>>(() =>
    Object.fromEntries(AUX_SURFACES.map(({ surface }) => [surface, {}])) as Record<
      AuxSurface,
      Record<string, string>
    >,
  )
  const [results, setResults] = useState<Partial<Record<AuxSurface, AuxResult>>>({})
  const [errors, setErrors] = useState<Partial<Record<AuxSurface, string>>>({})
  const [busy, setBusy] = useState<Partial<Record<AuxSurface, boolean>>>({})
  const audioUrls = useRef<Partial<Record<AuxSurface, string>>>({})

  useEffect(() => {
    const urls = audioUrls.current
    return () => {
      for (const url of Object.values(urls)) {
        if (url) URL.revokeObjectURL(url)
      }
    }
  }, [])

  function setField(surface: AuxSurface, key: string, value: string) {
    setForms((f) => ({ ...f, [surface]: { ...f[surface], [key]: value } }))
  }

  async function pickFile(surface: AuxSurface, file: File) {
    const file_b64 = await readFileAsBase64(file)
    setForms((f) => ({ ...f, [surface]: { ...f[surface], file_b64, filename: file.name } }))
  }

  async function run(surface: AuxSurface) {
    if (busy[surface]) return
    setBusy((b) => ({ ...b, [surface]: true }))
    setErrors((e) => ({ ...e, [surface]: "" }))
    try {
      let body = auxBodyFor(surface, forms[surface])
      if (surface === "rerank") {
        // documents has no legal single-string wire shape. auxBodyFor's
        // NUMERIC set is the only per-field special case it knows; the one
        // array-shaped field here is assembled by hand instead.
        const documents = (forms[surface].documents ?? "")
          .split("\n")
          .map((s) => s.trim())
          .filter((s) => s !== "")
        body = { ...body, body: { ...body.body, documents } }
      }
      const res = await postRaw("/api/playground/aux", body)
      const requestId = res.headers.get("X-Darkrouter-Request") ?? ""
      const result = await readAuxResult(surface, res, requestId, audioUrls)
      setResults((r) => ({ ...r, [surface]: result }))
    } catch (err) {
      setErrors((e) => ({ ...e, [surface]: (err as Error).message }))
    } finally {
      setBusy((b) => ({ ...b, [surface]: false }))
    }
  }

  return (
    <Tabs defaultValue={AUX_SURFACES[0].surface} className="p-6">
      <TabsList>
        {AUX_SURFACES.map(({ surface, label }) => (
          <TabsTrigger key={surface} value={surface}>
            {label}
          </TabsTrigger>
        ))}
      </TabsList>
      {AUX_SURFACES.map(({ surface, needsFile }) => (
        <TabsContent key={surface} value={surface} className="flex flex-col gap-4 pt-4">
          <AuxSurfaceForm
            surface={surface}
            needsFile={needsFile}
            form={forms[surface]}
            busy={busy[surface] ?? false}
            error={errors[surface] ?? ""}
            result={results[surface]}
            onField={(key, value) => setField(surface, key, value)}
            onFile={(file) => void pickFile(surface, file)}
            onRun={() => void run(surface)}
          />
        </TabsContent>
      ))}
    </Tabs>
  )
}

const COUNT_DIALECTS = ["anthropic", "gemini"] as const
type CountDialect = (typeof COUNT_DIALECTS)[number]

/** Token counting against the two dialects that have a native counting
 *  endpoint. There is no OpenAI option: that wire has no counting endpoint,
 *  and Task 3's handler refuses the dialect with a 400 rather than quietly
 *  substituting the local estimate for a reading. */
export function Count() {
  const [dialect, setDialect] = useState<CountDialect>("anthropic")
  const [model, setModel] = useState("")
  const [prompt, setPrompt] = useState("")
  const [result, setResult] = useState<CountResult | undefined>(undefined)
  const [error, setError] = useState("")
  const [busy, setBusy] = useState(false)

  async function run() {
    if (busy || model === "" || prompt === "") return
    setBusy(true)
    setError("")
    setResult(undefined)
    try {
      const res = await postRaw("/api/playground/count", { dialect, model, prompt })
      const body: unknown = await res.json()
      setResult(readCount(res, body))
    } catch (err) {
      setError((err as Error).message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="p-6">
      <Card className="flex max-w-md flex-col gap-3 p-4">
        <Select value={dialect} onValueChange={(v) => setDialect(v as CountDialect)}>
          <SelectTrigger>
            <SelectValue placeholder="dialect" />
          </SelectTrigger>
          <SelectContent>
            {COUNT_DIALECTS.map((d) => (
              <SelectItem key={d} value={d}>
                {d}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        <Input placeholder="model" value={model} onChange={(e) => setModel(e.target.value)} />
        <Textarea placeholder="Prompt" value={prompt} onChange={(e) => setPrompt(e.target.value)} />
        <Button onClick={() => void run()} disabled={busy || model === "" || prompt === ""}>
          {busy ? "Counting…" : "Count"}
        </Button>
        {error ? <p className="text-destructive text-sm">{error}</p> : null}
        {result ? (
          <p className="text-sm">
            {result.tokens} tokens
            {result.estimated
              ? " — estimated locally — no candidate provider speaks this counting dialect"
              : ""}
          </p>
        ) : null}
      </Card>
    </div>
  )
}
