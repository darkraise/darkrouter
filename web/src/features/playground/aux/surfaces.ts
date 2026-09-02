import { getCsrfToken, throwOnExecutorError } from "../../../lib/api"
import type {
  AuxBody, AuxSurface, CountResult, Model, PlaygroundDialect, Provider,
} from "../../../lib/api-types"

/**
 * The aux tools.
 *
 * `surface` is the executor's own wire vocabulary, which is plural and
 * endpoint-shaped. `catalogSurface` is `ir.Surface` — what the catalogue
 * stores and `/api/models` serves — which is singular and capability-shaped.
 * The two disagree for five of the six, so a model filter written against the
 * wire name silently matches nothing.
 *
 * `blurb` is what the tool does, in the operator's terms rather than the
 * endpoint's. The rail lists six names that mean little on their own —
 * "Rerank" and "Moderation" are jobs, not nouns most people carry around.
 */
export const AUX_SURFACES = [
  {
    surface: "count",
    catalogSurface: "llm",
    label: "Token Count",
    blurb: "Measure a prompt before sending",
    needsFile: false,
  },
  {
    surface: "embeddings",
    catalogSurface: "embedding",
    label: "Embeddings",
    blurb: "Turn text into a vector",
    needsFile: false,
  },
  {
    surface: "rerank",
    catalogSurface: "rerank",
    label: "Rerank",
    blurb: "Order documents by a query",
    needsFile: false,
  },
  {
    surface: "moderations",
    catalogSurface: "moderation",
    label: "Moderation",
    blurb: "Check text against policy",
    needsFile: false,
  },
  {
    surface: "images",
    catalogSurface: "image",
    label: "Images",
    blurb: "Draw from a prompt",
    needsFile: false,
  },
  {
    surface: "speech",
    catalogSurface: "tts",
    label: "Speech",
    blurb: "Read text aloud",
    needsFile: false,
  },
  {
    surface: "transcriptions",
    catalogSurface: "stt",
    label: "Transcription",
    blurb: "Write down what was said",
    needsFile: true,
  },
] as const

/** The catalogue's name for an aux surface, for the model filter. */
export function catalogSurfaceFor(surface: AuxSurface): string {
  return AUX_SURFACES.find((s) => s.surface === surface)?.catalogSurface ?? surface
}

export function surfaceInfo(surface: AuxSurface) {
  return AUX_SURFACES.find((s) => s.surface === surface) ?? AUX_SURFACES[0]
}

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

export type FieldSpec = {
  key: string
  label: string
  type?: "number"
  multiline?: boolean
  /** The one field a run is mostly about. It gets the composer's height and
   *  the Enter-to-run binding; the rest are settings beside it. */
  primary?: boolean
  placeholder?: string
  hint?: string
  options?: string[]
}

export type FormSurface = Exclude<AuxSurface, "transcriptions">

export const SURFACE_FIELDS: Record<FormSurface, FieldSpec[]> = {
  count: [
    { key: "dialect", label: "Dialect", options: ["anthropic", "gemini"] },
    { key: "prompt", label: "Prompt", multiline: true, primary: true,
      placeholder: "Text to count" },
  ],
  embeddings: [
    { key: "input", label: "Input", multiline: true, primary: true,
      placeholder: "Text to embed" },
    { key: "dimensions", label: "Dimensions", type: "number",
      hint: "Leave empty for the model's own" },
  ],
  rerank: [
    { key: "query", label: "Query", placeholder: "What the documents are ranked against" },
    { key: "documents", label: "Documents", multiline: true, primary: true,
      placeholder: "One per line" },
  ],
  moderations: [
    { key: "input", label: "Input", multiline: true, primary: true,
      placeholder: "Text to check" },
  ],
  images: [
    { key: "prompt", label: "Prompt", multiline: true, primary: true,
      placeholder: "What to draw" },
    { key: "n", label: "Count", type: "number", hint: "How many to return" },
    { key: "size", label: "Size", placeholder: "1024x1024" },
  ],
  speech: [
    { key: "input", label: "Input", multiline: true, primary: true,
      placeholder: "Text to read aloud" },
    { key: "voice", label: "Voice", placeholder: "alloy" },
  ],
}

/** What a run produced, in the shape its tool draws rather than as raw JSON. */
export type AuxOutcome =
  | { kind: "count"; tokens: number; estimated: boolean }
  | { kind: "embedding"; vector: number[] }
  | { kind: "rerank"; ranked: { index: number; score: number; text: string }[] }
  | {
      kind: "moderation"
      flagged: boolean
      scores: { name: string; score: number; flagged: boolean }[]
    }
  | { kind: "image"; urls: string[] }
  | { kind: "audio"; url: string; bytes: number }
  | { kind: "transcript"; text: string }
  | { kind: "json"; json: unknown }

/** One run of one tool, kept so a second can be compared against it. */
export type AuxRun = {
  id: number
  at: number
  /** What was asked, in one line, for the run's own heading. */
  summary: string
  requestId: string
  outcome: AuxOutcome
}

/**
 * The one fetch call this feature makes. Callers need the raw `Response` for
 * its headers and, for speech, its bytes — exactly what `api.post` cannot
 * give them, which is why this exists instead of reusing it. A non-OK
 * response goes through the same session-death classification `stream()`
 * uses, since this hits the same executor and the same dialect-writer error
 * shape.
 */
export async function postAux(body: unknown, signal?: AbortSignal): Promise<Response> {
  const res = await fetch("/api/playground/aux", {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      "X-CSRF-Token": getCsrfToken(),
      "Sec-Fetch-Site": "same-origin",
    },
    body: JSON.stringify(body),
    credentials: "same-origin",
    signal,
  })
  if (!res.ok) return await throwOnExecutorError(res)
  return res
}

export function countBodyFor(form: Record<string, string>) {
  return { model: form.model, dialect: form.dialect, prompt: form.prompt }
}

export async function postCount(
  form: Record<string, string>,
  signal?: AbortSignal,
): Promise<Response> {
  const res = await fetch("/api/playground/count", {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      "X-CSRF-Token": getCsrfToken(),
      "Sec-Fetch-Site": "same-origin",
    },
    body: JSON.stringify(countBodyFor(form)),
    credentials: "same-origin",
    signal,
  })
  if (!res.ok) return await throwOnExecutorError(res)
  return res
}

export async function readCount(res: Response): Promise<{ kind: "count" } & CountResult> {
  const body = await res.json() as { input_tokens?: number; totalTokens?: number }
  const tokens = body.input_tokens ?? body.totalTokens
  if (typeof tokens !== "number" || !Number.isFinite(tokens)) {
    throw new Error("token count response did not contain a count")
  }
  return {
    kind: "count",
    tokens,
    estimated: res.headers.get("X-Darkrouter-Estimated") === "true",
  }
}

export function isAuxReady(
  surface: AuxSurface,
  form: Record<string, string>,
): boolean {
  return auxBlocker(surface, form) === null
}

/** The first thing still missing before a run can go, said so the disabled
 *  button does not have to be read against the whole form. Null once the
 *  run is ready. */
export function auxBlocker(surface: AuxSurface, form: Record<string, string>): string | null {
  if ((form.model ?? "").trim() === "") return "Name a model to run against."
  if (surface === "transcriptions") {
    return (form.file_b64 ?? "") !== "" ? null : "Choose an audio file to transcribe."
  }
  if (surface === "rerank" && (form.query ?? "").trim() === "") return "Add a query to rank against."
  if (surface === "count" && (form.dialect ?? "") === "") {
    return "Choose a dialect: the count is made by the provider, and only the anthropic and gemini wires count."
  }
  const primary = SURFACE_FIELDS[surface as FormSurface].find((field) => field.primary)
  if (primary === undefined) return null
  if ((form[primary.key] ?? "").trim() !== "") return null
  return `Add ${primary.label === "Prompt" ? "a prompt" : `the ${primary.label.toLowerCase()}`} to send.`
}

/** Which counting wire a provider speaks. Vertex fronts Gemini models, so it
 *  counts the way Gemini does; the OpenAI-compatible kinds have no count
 *  endpoint at all. */
const COUNT_DIALECT_BY_KIND: Record<string, PlaygroundDialect> = {
  anthropic: "anthropic",
  gemini: "gemini",
  vertex: "gemini",
}

/** The counting dialect a model's own provider speaks, or null when none of
 *  its providers counts natively and the choice has to stay the operator's. */
export function countDialectFor(
  model: string,
  models: Pick<Model, "model" | "providers">[],
  providers: Pick<Provider, "id" | "kind">[],
): PlaygroundDialect | null {
  const row = models.find((m) => m.model === model)
  if (row === undefined) return null
  for (const id of row.providers) {
    const kind = providers.find((p) => p.id === id)?.kind
    const dialect = kind === undefined ? undefined : COUNT_DIALECT_BY_KIND[kind]
    if (dialect !== undefined) return dialect
  }
  return null
}

export function readFileAsBase64(file: File): Promise<string> {
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

/** The documents a rerank run was given, which the response indexes into.
 *  The wire omits `document` unless the client asked for it, so the text has
 *  to come back from what was sent. */
export function documentLines(raw: string): string[] {
  return raw.split("\n").map((s) => s.trim()).filter((s) => s !== "")
}

/**
 * The response, turned into the shape its tool draws.
 *
 * Every branch falls back to `json` rather than throwing: an unfamiliar
 * response is still a result, and a tree of it beats an error message that
 * says the run failed when it did not.
 */
export async function outcomeOfResponse(
  surface: AuxSurface,
  res: Response,
  form: Record<string, string>,
  revoke: (url: string) => void,
): Promise<AuxOutcome> {
  if (surface === "speech") {
    const blob = await res.blob()
    const url = URL.createObjectURL(blob)
    revoke(url)
    return { kind: "audio", url, bytes: blob.size }
  }

  const json = (await res.json()) as Record<string, unknown>

  if (surface === "embeddings") {
    const data = json.data as { embedding?: number[] }[] | undefined
    const vector = data?.[0]?.embedding
    if (vector) return { kind: "embedding", vector }
  }

  if (surface === "images") {
    const data = (json.data as { b64_json?: string; url?: string }[] | undefined) ?? []
    const urls = data.map((d) =>
      d.b64_json ? `data:image/png;base64,${d.b64_json}` : (d.url ?? ""),
    )
    if (urls.length > 0) return { kind: "image", urls }
  }

  if (surface === "rerank") {
    const sent = documentLines(form.documents ?? "")
    const results = json.results as
      | { index: number; relevance_score: number; document?: { text?: string } }[]
      | undefined
    if (results) {
      return {
        kind: "rerank",
        ranked: results.map((r) => ({
          index: r.index,
          score: r.relevance_score,
          // The wire's own text when it sent one, and the line that was
          // submitted when it did not.
          text: r.document?.text ?? sent[r.index] ?? `document ${r.index}`,
        })),
      }
    }
  }

  if (surface === "moderations") {
    const results = json.results as
      | {
          flagged?: boolean
          categories?: Record<string, boolean>
          category_scores?: Record<string, number>
        }[]
      | undefined
    const first = results?.[0]
    if (first) {
      const cats = first.categories ?? {}
      const scores = first.category_scores ?? {}
      // Ordered by score rather than by name: the reason something was
      // flagged is the top of this list, and alphabetical order buries it.
      const rows = Object.keys({ ...cats, ...scores })
        .map((name) => ({
          name,
          score: scores[name] ?? 0,
          flagged: cats[name] ?? false,
        }))
        .sort((a, b) => b.score - a.score)
      return { kind: "moderation", flagged: first.flagged ?? false, scores: rows }
    }
  }

  if (surface === "transcriptions" && typeof json.text === "string") {
    return { kind: "transcript", text: json.text }
  }

  return { kind: "json", json }
}

/** The one-line heading a run carries, so a scrolled-back result still says
 *  what it answered. */
export function runSummary(surface: AuxSurface, form: Record<string, string>): string {
  if (surface === "transcriptions") return form.filename ?? "audio file"
  if (surface === "rerank") return form.query || "no query"
  const primary = SURFACE_FIELDS[surface as FormSurface]?.find((f) => f.primary)
  const text = (primary ? form[primary.key] : "") ?? ""
  const flat = text.trim().replace(/\s+/g, " ")
  if (flat === "") return "empty input"
  return flat.length > 80 ? `${flat.slice(0, 79)}…` : flat
}
