import { useEffect, useRef, useState } from "react"
import { Card } from "darkraise-ui"
import {
  ResizableHandle,
  ResizablePanel,
  ResizablePanelGroup,
} from "darkraise-ui/components/resizable"
import { ModelCombobox, useModelCandidates } from "../../shell/model-combobox"
import { EmptyState } from "../../shell/empty-state"
import { useModels, useProviders } from "../../../lib/queries"
import { ToolRail } from "./tool-rail"
import { ToolInputs } from "./tool-inputs"
import { RunCard } from "./results"
import { RunReadings } from "./run-readings"
import {
  AUX_SURFACES,
  auxBodyFor,
  catalogSurfaceFor,
  countDialectFor,
  documentLines,
  postAux,
  postCount,
  readFileAsBase64,
  readOutcome,
  readCount,
  runSummary,
  surfaceInfo,
  type AuxRun,
} from "./surfaces"
import type { AuxSurface } from "../../../lib/api-types"

/**
 * The auxiliary surfaces, as tools rather than as a form page.
 *
 * They were six tabs over a stack of unlabelled full-width boxes and one
 * page-wide Run bar, with the result appended underneath and the rest of the
 * screen empty. That is a shape nobody built on purpose: it is what a form
 * becomes when it is added to a tab at a time.
 *
 * The three regions are Chat's, deliberately — a rail of tools where Chat has
 * a rail of conversations, an island whose foot holds what is being asked,
 * and a column of readings on the right. An operator moving between the two
 * surfaces should not have to learn a second layout, and every one of these
 * tools is the same act Chat performs: name a model, send something, read
 * what came back.
 *
 * Runs accumulate per tool for the session. Comparing this embedding against
 * the last one is the reason to run it twice, and a panel that replaced its
 * result on every run made that impossible. They are not stored: nothing here
 * is written to the database, and the request log already keeps the permanent
 * record of every call these make.
 */
export function AuxMode({ active: isActive = true }: { active?: boolean }) {
  const [active, setActive] = useState<AuxSurface>(AUX_SURFACES[0].surface)
  const [forms, setForms] = useState<Partial<Record<AuxSurface, Record<string, string>>>>({})
  const [runs, setRuns] = useState<Partial<Record<AuxSurface, AuxRun[]>>>({})
  const [errors, setErrors] = useState<Partial<Record<AuxSurface, string>>>({})
  const [busy, setBusy] = useState<Partial<Record<AuxSurface, boolean>>>({})

  // Every object URL this surface has minted, revoked together on unmount.
  // A run replaces the audio on screen but not the blob behind the previous
  // one, and the history keeps both playable.
  const objectUrls = useRef<string[]>([])
  const controllers = useRef(new Map<AuxSurface, AbortController>())
  const runId = useRef(0)
  useEffect(() => {
    const urls = objectUrls.current
    const liveControllers = controllers.current
    return () => {
      for (const url of urls) URL.revokeObjectURL(url)
      for (const controller of liveControllers.values()) controller.abort()
      liveControllers.clear()
    }
  }, [])

  useEffect(() => {
    if (isActive) return
    for (const controller of controllers.current.values()) controller.abort()
    controllers.current.clear()
  }, [isActive])

  const info = surfaceInfo(active)
  const form = forms[active] ?? {}
  const shown = runs[active] ?? []

  // No aliases: an alias resolves to whatever chain it names, and a chain is
  // built out of chat models. Naming one on an embeddings form would route a
  // request the surface cannot serve.
  const { candidates, loading } = useModelCandidates({
    aliases: false,
    surface: catalogSurfaceFor(active),
  })
  // Both already cached by the screens that own them; nothing refetches for
  // this. They answer which counting wire a model's provider speaks.
  const catalog = useModels()
  const providers = useProviders()

  function setField(surface: AuxSurface, key: string, value: string) {
    setForms((f) => ({ ...f, [surface]: { ...(f[surface] ?? {}), [key]: value } }))
  }

  /** Naming a model on Token Count also picks the dialect its provider counts
   *  in. The count has no OpenAI wire, so the default the operator would
   *  otherwise have to know is the one thing the catalogue can tell them. */
  function setCountModel(model: string) {
    const dialect = countDialectFor(
      model,
      catalog.data?.models ?? [],
      providers.data?.providers ?? [],
    )
    setForms((f) => ({
      ...f,
      count: { ...(f.count ?? {}), model, ...(dialect === null ? {} : { dialect }) },
    }))
  }

  async function pickFile(surface: AuxSurface, file: File) {
    const file_b64 = await readFileAsBase64(file)
    setForms((f) => ({
      ...f,
      [surface]: { ...(f[surface] ?? {}), file_b64, filename: file.name },
    }))
  }

  async function run(surface: AuxSurface) {
    if (busy[surface]) return
    const current = forms[surface] ?? {}
    setBusy((b) => ({ ...b, [surface]: true }))
    setErrors((e) => ({ ...e, [surface]: "" }))
    const controller = new AbortController()
    controllers.current.set(surface, controller)
    try {
      let res: Response
      let outcome
      if (surface === "count") {
        res = await postCount(current, controller.signal)
        outcome = await readCount(res)
      } else {
        let body = auxBodyFor(surface, current)
        if (surface === "rerank") {
        // documents has no legal single-string wire shape. auxBodyFor's
        // NUMERIC set is the only per-field special case it knows; the one
        // array-shaped field here is assembled by hand instead.
          body = { ...body, body: { ...body.body, documents: documentLines(current.documents ?? "") } }
        }
        res = await postAux(body, controller.signal)
        outcome = await readOutcome(surface, res, current, (url) => {
          objectUrls.current.push(url)
        })
      }
      const requestId = res.headers.get("X-Darkrouter-Request") ?? ""
      runId.current += 1
      const entry: AuxRun = {
        id: runId.current,
        at: Date.now(),
        summary: runSummary(surface, current),
        requestId,
        outcome,
      }
      // Newest first: the run just made is the one being read, and a column
      // that grows downward puts it off the bottom of the panel.
      setRuns((r) => ({ ...r, [surface]: [entry, ...(r[surface] ?? [])] }))
    } catch (err) {
      if ((err as Error).name !== "AbortError") {
        setErrors((e) => ({ ...e, [surface]: (err as Error).message }))
      }
    } finally {
      if (controllers.current.get(surface) === controller) {
        controllers.current.delete(surface)
      }
      setBusy((b) => ({ ...b, [surface]: false }))
    }
  }

  const counts = Object.fromEntries(
    Object.entries(runs).map(([surface, list]) => [surface, list?.length ?? 0]),
  ) as Partial<Record<AuxSurface, number>>

  return (
    <ResizablePanelGroup className="flex min-h-0 flex-1 gap-0 px-6 pb-6">
      <ResizablePanel defaultSize={20} minSize={14} maxSize={40} className="flex min-h-0 flex-col">
        <ToolRail active={active} onSelect={setActive} runCounts={counts} />
      </ResizablePanel>

      <ResizableHandle withHandle className="mx-2" />

      <ResizablePanel className="flex min-h-0 min-w-0 flex-col gap-4">
        {/* The model this tool will send to, on its own island above the
            work — the same place Chat names the model answering. Narrowed to
            this surface: an embeddings box offering a chat model is offering
            a request the executor will refuse. */}
        <Card className="flex shrink-0 items-center gap-3 p-3">
          <span className="shrink-0 text-sm font-medium">{info.label}</span>
          <ModelCombobox
            label={`${info.label} model`}
            placeholder="model"
            value={form.model ?? ""}
            onChange={(model) =>
              active === "count" ? setCountModel(model) : setField(active, "model", model)
            }
            candidates={candidates}
            loading={loading}
            className="min-w-0 flex-1"
          />
        </Card>

        <div className="flex min-h-0 flex-1 gap-4">
        <Card className="flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden p-0">
          <div className="mx-auto flex min-h-0 w-full max-w-3xl flex-1 flex-col">
            <div className="min-h-0 flex-1 overflow-y-auto px-6 py-4">
              {errors[active] ? (
                <p role="alert" className="pb-4 text-sm text-[hsl(var(--destructive))]">
                  {errors[active]}
                </p>
              ) : null}

              {shown.length === 0 ? (
                <div className="flex h-full items-center justify-center">
                  <EmptyState
                    title={`${info.label} — ${info.blurb.toLowerCase()}`}
                    hint={
                      form.model
                        ? "Fill in what to send and run it. Every run stays here for the session so you can compare two."
                        : "Name a model above, then fill in what to send. The router resolves it the same way it resolves one from a client."
                    }
                  />
                </div>
              ) : (
                <div className="flex flex-col gap-6">
                  {shown.map((r) => (
                    <RunCard key={r.id} run={r} />
                  ))}
                </div>
              )}
            </div>
          </div>

          {/* Inside the island, under no rule, for the reason Chat's composer
              is: the fields draw their own borders and a divider a few pixels
              over them says twice what one line already said. */}
          <div className="shrink-0">
            <div className="mx-auto w-full max-w-3xl px-6 py-4">
              <ToolInputs
                surface={active}
                needsFile={info.needsFile}
                form={form}
                busy={busy[active] ?? false}
                onField={(key, value) => setField(active, key, value)}
                onFile={(file) => void pickFile(active, file)}
                onRun={() => void run(active)}
              />
            </div>
          </div>
        </Card>

        {/* The readings and the trace, in the column Chat gives Consumption.
            An aux call answers in one response with no stream, so unlike a
            chat turn there is nothing to count on the way past -- these come
            from the trace the gateway wrote. */}
        <div className="hidden w-72 shrink-0 flex-col gap-4 overflow-y-auto lg:flex">
          <RunReadings run={shown[0]} />
        </div>
        </div>
      </ResizablePanel>
    </ResizablePanelGroup>
  )
}
