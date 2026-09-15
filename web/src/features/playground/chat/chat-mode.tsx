import { useEffect, useRef, useState } from "react"
import { Button, Card, Sheet, SheetContent, SheetHeader, SheetTitle } from "darkraise-ui"
import { ResizableHandle, ResizablePanel, ResizablePanelGroup } from "darkraise-ui/components/resizable"
import { useSearch } from "@tanstack/react-router"
import { useQueryClient } from "@tanstack/react-query"
import { keys, usePlaygroundConversation, usePlaygroundConversations, useTrace } from "../../../lib/queries"
import {
  configOfConversation,
  messagesOfTurns,
  routesOfTurns,
  titleFromPrompt,
  useAppendTurn,
  useCreateConversation,
  useDeleteConversation,
  useUpdateConversation,
} from "../lib/conversations"
import { useChatRun, type CompletedTurn } from "../lib/use-chat-run"
import { emptyConfig, type PlaygroundConfig } from "../config"
import { requestProblem, seedFromTrace } from "../lib/request"
import { ConfigPane } from "../config-pane/config-pane"
import { NO_METRICS, type StreamMetrics } from "../metrics"
import { TokenPanel, consumptionOf } from "../token-panel"
import { Transcript } from "../transcript"
import { Composer } from "../composer"
import { HistoryRail } from "./history-rail"
import { ConversationHeader } from "./conversation-header"
import { NewConversationDialog } from "./new-conversation-dialog"
import { ApiError } from "../../../lib/api"
import type {
  PlaygroundConversation,
  PlaygroundConversationDetail,
  RequestTrace,
} from "../../../lib/api-types"
import { PanelLeft } from "lucide-react"

/**
 * A conversation that is still there tomorrow.
 *
 * Three regions, and islands inside the middle one: the conversations panel
 * on the left, the conversation itself, and the request pane on the right.
 * Separating them says which controls belong to the thread and which to the
 * message being typed — a distinction one continuous column left the reader
 * to work out.
 *
 * This is where Lab's Single surface went. A playground that made you choose
 * between "a conversation that is kept" and "a request you can actually tune"
 * was two half-screens: an operator who wanted a temperature had to abandon
 * their transcript to get one, and one who wanted a transcript sent every turn
 * at the provider's defaults. The two are one screen now, and the seam is
 * time rather than place — every setting is open until the first message, and
 * fixed after it.
 *
 * Fixed rather than merely discouraged, because a conversation is a record.
 * Every answer above was produced under these settings, and a model or a
 * temperature that could still be changed would make the thread a transcript
 * of a request that was never sent.
 *
 * The saving is deliberately invisible. A history rail behind an explicit Save
 * button does not get used, and a conversation the operator has to remember to
 * keep is one they will lose. What that costs is stated in spec section 8.2:
 * this is the first place darkrouter retains prompt text automatically and in
 * bulk. A saved preset already keeps its system prompt, and neither the key nor
 * the purge reaches that one.
 */
/** The name a conversation carries until it has one. Anything else in the
 *  field is the operator's own, and outranks a title derived from the prompt. */
const UNTITLED = "New chat"

/** The thread an exchange was sent from, as of the render that sent it. */
type ExchangeOwner = { id: string; selection: number; title: string; config: PlaygroundConfig }

type PendingExchange = {
  turn: CompletedTurn
  owner: ExchangeOwner
  /** The conversation it is stored in, once one exists. */
  id: string
  /** Set once the question is stored, so a retry sends only the answer. */
  userSeq: number | null
  /** Its last save failed in a way that may pass on retry. */
  failed: boolean
}

/** Exchanges of one thread share a key before and after its conversation is
 *  created, so one still waiting on the create cannot jump ahead of one that
 *  already has the id. */
function queueOf(exchange: PendingExchange): string {
  return exchange.id !== "" ? exchange.id : `selection:${exchange.owner.selection}`
}

/** A held exchange, as the banner needs to describe it. */
type HeldExchange = { id: string; selection: number; failed: boolean; questionStored: boolean }

/** A network failure, a server fault, a timeout or a rate limit can pass on a
 *  later try; any other refusal answers the same way every time. */
function mayPassOnRetry(err: unknown): boolean {
  if (!(err instanceof ApiError)) return true
  return err.status >= 500 || err.status === 408 || err.status === 429
}

export function ChatMode({ active = true }: { active?: boolean }) {
  const [config, setConfig] = useState<PlaygroundConfig>(emptyConfig)
  const [activeId, setActiveId] = useState("")
  const [loadedId, setLoadedId] = useState("")
  const [title, setTitle] = useState(UNTITLED)
  const [metrics, setMetrics] = useState<StreamMetrics>(NO_METRICS)
  const [seededFrom, setSeededFrom] = useState<string | undefined>(undefined)
  // What the dialog is showing, and what it opens on. Held apart from `config`
  // so a draft being edited in the dialog is not the thing the screen behind it
  // would send if the operator closed it and typed.
  const [settingsOpen, setSettingsOpen] = useState(false)
  const [settingsSeed, setSettingsSeed] = useState<PlaygroundConfig>(emptyConfig)
  // How the dialog was opened, not whether a row exists. A conversation is not
  // stored until its first turn is, so "has an id" is false for exactly the
  // case the actions menu exists to serve — a thread set up and not yet sent.
  const [settingsAmending, setSettingsAmending] = useState(false)
  const [historyOpen, setHistoryOpen] = useState(false)

  const queryClient = useQueryClient()
  const { data: conversations } = usePlaygroundConversations()
  const detail = usePlaygroundConversation(activeId, { enabled: activeId !== "" })
  const selectionPending = activeId !== "" && loadedId !== activeId
  const selectionFailed = selectionPending && detail.isError
  const create = useCreateConversation()
  const append = useAppendTurn()
  const update = useUpdateConversation()
  const remove = useDeleteConversation()

  // Mutations that happen after a send use refs so they see the latest title
  // and settings. Conversation ownership is the exception: the callback below
  // captures activeId when the send begins, so selecting another thread while
  // it streams cannot redirect its stored turn.
  const conversationRef = useRef("")
  const configRef = useRef(config)
  // A config commit can be scheduled by the header and fire a moment later, so
  // the title it carries is read when it fires rather than when it was queued:
  // a rename in between would otherwise be undone by the write that follows it.
  const titleRef = useRef(title)
  // Written after the commit rather than during the render. Every reader is a
  // callback that runs later -- a send completing, a scheduled commit -- so
  // what they need is the last rendered value, which is what a render React
  // went on to discard did not produce.
  useEffect(() => {
    configRef.current = config
    titleRef.current = title
  })
  // The create is memoized on its own promise rather than on the id it
  // resolves to: two exchanges completing while the first create is still in
  // flight would both read an empty conversationRef and make two
  // conversations for one thread. Keyed by selection, because a thread left
  // mid-answer still creates its conversation after the next thread has
  // started, and that create is not the next thread's.
  const creating = useRef(new Map<number, Promise<PlaygroundConversation>>())
  // What each selection's create resolved to. An exchange sent before the id
  // arrived and finished after it would otherwise queue under the selection
  // while its thread's earlier exchanges queue under the id, and could be
  // saved past one of them that is held.
  const createdIds = useRef(new Map<number, string>())
  // Changes whenever the operator chooses which conversation owns the
  // screen. A create may still finish after that choice; it should persist
  // the completed turn, but it must not move the screen back to the thread it
  // created.
  const selectionGeneration = useRef(0)
  // The same count as of the render a send starts from, which is the thread
  // its turn belongs to however far the ref has moved on by the time it ends.
  const [selection, setSelection] = useState(0)

  // Every completed exchange, oldest first, until it is stored whole. One
  // drain saves them one exchange at a time: seq is assigned in arrival
  // order and an exchange is two writes, so saving per write let a second
  // exchange's question land between the first one's question and answer.
  // An exchange records how far it got, so a retry resumes rather than
  // storing its question twice.
  const backlog = useRef<PendingExchange[]>([])
  const draining = useRef(false)
  // A drain asked for while one runs starts another pass once it ends rather
  // than being dropped.
  const drainAgain = useRef(false)
  // What is held, as of the last drain. Later exchanges of a conversation wait
  // behind one of its failed ones, because saving past it would scramble the
  // order too; other conversations' exchanges do not.
  const [unsaved, setUnsaved] = useState<HeldExchange[]>([])

  function publishBacklog() {
    setUnsaved(
      backlog.current.map((exchange) => ({
        id: exchange.id,
        selection: exchange.owner.selection,
        failed: exchange.failed,
        questionStored: exchange.userSeq !== null,
      })),
    )
  }

  function persistTurn(turn: CompletedTurn, owner: ExchangeOwner) {
    const id = owner.id || (createdIds.current.get(owner.selection) ?? "")
    backlog.current.push({ turn, owner, id, userSeq: null, failed: false })
    void drain()
  }

  async function drain() {
    if (draining.current) {
      drainAgain.current = true
      return
    }
    draining.current = true
    try {
      for (;;) {
        // A failed exchange keeps its conversation held until the operator
        // retries or discards it. Any drain may start a pass -- another
        // conversation's exchange finishing does -- and retrying on that
        // would repeat a failing request and its error toast each time.
        const blocked = new Set(backlog.current.filter((e) => e.failed).map(queueOf))
        const next = backlog.current.find((exchange) => !blocked.has(queueOf(exchange)))
        if (next === undefined) {
          if (!drainAgain.current) break
          drainAgain.current = false
          continue
        }
        next.failed = false
        try {
          await saveExchange(next)
        } catch (err) {
          // useApiMutation has already reported it through the toaster.
          // Losing a saved turn must not take the transcript on screen down
          // with it. A refusal -- saving switched off, the conversation
          // deleted -- will refuse again, so only a failure that can pass is
          // held for another try.
          if (mayPassOnRetry(err)) {
            next.failed = true
            continue
          }
        }
        backlog.current = backlog.current.filter((exchange) => exchange !== next)
      }
    } finally {
      draining.current = false
      publishBacklog()
    }
  }

  function retryFailed() {
    for (const exchange of backlog.current) exchange.failed = false
    void drain()
  }

  // Only exchanges whose last save failed: one mid-save has failed cleared,
  // and one merely waiting behind a failure was never tried. One whose
  // question is stored stays, since there is no way to remove that question
  // and dropping the answer would leave the next question straight after it.
  function discardFailed() {
    backlog.current = backlog.current.filter(
      (exchange) =>
        !(exchange.failed && exchange.userSeq === null && onScreen(exchange.id, exchange.owner.selection)),
    )
    publishBacklog()
    void drain()
  }

  /** Whether an exchange belongs to the conversation the screen shows. */
  function onScreen(id: string, owner: number): boolean {
    return id !== "" ? id === activeId : owner === selection
  }

  async function saveExchange(exchange: PendingExchange) {
    const { turn, owner } = exchange
    try {
      // Ownership is captured by the render that starts the request. Reading
      // conversationRef here would file a slow answer under whichever thread
      // the operator selected while it was still streaming.
      let id = exchange.id
      if (id === "") {
        let pending = creating.current.get(owner.selection)
        if (pending === undefined) {
          // The refs carry a rename made while the answer streamed, but once
          // another thread has been chosen they hold that thread's title and
          // settings instead.
          const onScreen = selectionGeneration.current === owner.selection
          const title = onScreen ? titleRef.current : owner.title
          pending = create.mutateAsync({
            title: title === UNTITLED ? titleFromPrompt(turn.prompt) : title,
            config: onScreen ? configRef.current : owner.config,
          })
          creating.current.set(owner.selection, pending)
        }
        const made = await pending
        id = made.id
        createdIds.current.set(owner.selection, id)
        for (const waiting of backlog.current) {
          if (waiting.id === "" && waiting.owner.selection === owner.selection) waiting.id = id
        }
        exchange.id = id
        if (selectionGeneration.current === owner.selection) {
          conversationRef.current = id
          setActiveId(id)
          // Marked loaded at creation, so the read below does not fetch the row
          // that was just written and replace the live transcript with it.
          setLoadedId(id)
          setTitle(made.title)
        }
      }
      if (exchange.userSeq === null) {
        const user = await append.mutateAsync({
          id, role: "user", content: turn.prompt, requestId: "",
        })
        exchange.userSeq = user.seq
      }
      const userSeq = exchange.userSeq
      const assistant = await append.mutateAsync({
        id, role: "assistant", content: turn.answer, requestId: turn.requestId,
      })
      // The cached detail learns the turns it was just sent. Left stale, a
      // conversation left and reopened loads its old transcript from the
      // cache, and the refetch that follows is ignored because the id has
      // not changed — the answer just given vanishes until a reload.
      const at = new Date().toISOString()
      queryClient.setQueryData<PlaygroundConversationDetail>(
        keys.playgroundConversation(id),
        (old) =>
          old && {
            ...old,
            updated_at: at,
            preview: turn.prompt,
            messages: [
              ...old.messages,
              { seq: userSeq, role: "user", content: turn.prompt, request_id: "", created_at: at },
              {
                seq: assistant.seq,
                role: "assistant",
                content: turn.answer,
                request_id: turn.requestId,
                created_at: at,
              },
            ],
          },
      )
      void queryClient.invalidateQueries({ queryKey: keys.playgroundConversation(id) })
    } catch (err) {
      // Cleared so a failed create does not make every later send await the
      // same rejected promise.
      if (exchange.id === "") creating.current.delete(owner.selection)
      throw err
    }
  }

  const run = useChatRun(
    config,
    setMetrics,
    (turn) => persistTurn(turn, { id: activeId, selection, title, config }),
  )

  useEffect(() => {
    if (!active) run.stop()
    // `run` is recreated on every render; visibility is the event that ends
    // an in-flight request, and stop reads the current controller through its
    // ref.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [active])

  // What fixes the settings is a turn existing, not the send that made it:
  // a conversation reopened from the rail has turns and no send behind it,
  // and its settings are every bit as committed to. A send that ended with
  // nothing said is on screen but in no conversation, so it fixes nothing.
  const locked = run.history.length > 0

  // The trace drawer's "Open in playground" arrives as ?seed=. It carried
  // its model and dialect into Lab's request pane, which is this screen now.
  const search = useSearch({ strict: false })
  const seed = search.seed
  const trace = useTrace(seed ?? "", { enabled: seed !== undefined })

  // Applied once per seed, and never over a conversation: a seed sets up a
  // fresh request, and stomping the model of a thread the operator opened
  // from the rail would rewrite what its answers were produced under.
  // `seededFrom` is what makes it once: the guard is false on the render the
  // adjustment itself causes.
  if (trace.data && seed !== undefined && seededFrom !== seed && run.messages.length === 0) {
    setConfig((prev) => ({ ...prev, ...seedFromTrace(trace.data as RequestTrace) }))
    setSeededFrom(seed)
  }

  const seedNote =
    seed !== undefined && run.messages.length === 0
      ? // capture.bodies has a retention sweep and no writer, so a trace
        // carries no prompt text — the model and dialect are all a seeded
        // run can restore. Stated here rather than left for the operator to
        // discover from a transcript that is silently empty.
        seededFrom === seed
        ? `Seeded from trace ${seed}: model and dialect carried over. The original prompt was not retained and is not recoverable.`
        : trace.isError
          ? `Trace ${seed} could not be loaded, so nothing was seeded.`
          : `Loading trace ${seed}…`
      : undefined

  // Applied once per conversation: re-firing would stomp on turns the operator
  // has typed since it was opened.
  //
  // The one place on this screen that still seeds state from an effect. It
  // cannot be an adjustment during render, because the seeding is inseparable
  // from `run.load`, which aborts whatever is streaming: a render React goes
  // on to discard would kill a live request. Splitting the two apart is worse
  // still -- `loadedId` is what `selectionPending` reads, so moving it a
  // commit ahead of the transcript paints the new conversation as loaded with
  // the previous one's turns under it.
  /* eslint-disable react-hooks/set-state-in-effect -- see above */
  useEffect(() => {
    if (!detail.data || detail.data.id === loadedId) return
    run.load(messagesOfTurns(detail.data.messages), routesOfTurns(detail.data.messages))
    setConfig(configOfConversation(detail.data))
    setTitle(detail.data.title)
    setLoadedId(detail.data.id)
    // useChatRun returns a fresh object each render, so listing run would make
    // this fire every render rather than once per conversation.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [detail.data, loadedId])
  /* eslint-enable react-hooks/set-state-in-effect */

  function startNew() {
    selectionGeneration.current += 1
    setSelection(selectionGeneration.current)
    // Seeded from the conversation being left rather than from the defaults:
    // the model an operator has been working with is almost always the one
    // they want next, and every value it carries is on screen in the dialog
    // rather than inherited invisibly. Cancel takes none of it.
    setSettingsSeed(config)
    conversationRef.current = ""
    setActiveId("")
    setLoadedId("")
    setTitle(UNTITLED)
    setMetrics(NO_METRICS)
    setConfig(emptyConfig())
    run.load([], {})
    setSettingsAmending(false)
    setSettingsOpen(true)
  }

  /** The same dialog, reopened on a conversation that has not sent anything
   *  yet. Without it a mistyped temperature could only be fixed by starting
   *  the thread over, since the pane beside the transcript no longer edits. */
  function amendSettings() {
    setSettingsSeed(config)
    setSettingsAmending(true)
    setSettingsOpen(true)
  }

  function applySettings(next: PlaygroundConfig) {
    setConfig(next)
    commitConfig(next)
  }

  function select(id: string) {
    if (id === activeId) return
    selectionGeneration.current += 1
    setSelection(selectionGeneration.current)
    conversationRef.current = id
    setActiveId(id)
  }

  /** Model, dialect and the system prompt are stored with the conversation, so
   *  changing one part-way through moves the row rather than only the screen.
   *  The turns that came before stay: each answer's route line already records
   *  what actually served it.
   *
   *  Called only for a value the operator has settled on. The header updates
   *  the screen on every keystroke without coming through here. */
  function commitConfig(next: PlaygroundConfig) {
    if (conversationRef.current !== "") {
      update.mutate({ id: conversationRef.current, title: titleRef.current, config: next })
    }
  }

  function retitle(next: string) {
    setTitle(next)
    if (conversationRef.current !== "") {
      update.mutate({ id: conversationRef.current, title: next, config })
    }
  }

  function removeConversation(c: PlaygroundConversation) {
    remove.mutate({ id: c.id, title: c.title })
    if (c.id === conversationRef.current) startNew()
  }

  return (
    <>
    <ResizablePanelGroup className="flex min-h-0 flex-1 gap-0 px-6 pb-6">
      {/* Resizable rather than fixed at 260px: how much of the screen the
          retrieval deserves depends on how long the titles are and how many
          there are, and only the operator looking at them knows. The floor
          keeps a title readable; the ceiling keeps this a rail rather than a
          second transcript. */}
      <ResizablePanel
        defaultSize={20}
        minSize={12}
        maxSize={40}
        className="!hidden min-h-0 flex-col lg:!flex"
      >
        <HistoryRail
          conversations={conversations ?? []}
          activeId={activeId}
          onSelect={select}
          onNew={startNew}
          onDelete={removeConversation}
        />
      </ResizablePanel>

      <ResizableHandle withHandle className="mx-2 hidden lg:flex" />

      <ResizablePanel className="flex min-h-0 min-w-0 flex-col gap-4">
        <div className="lg:hidden">
          <Button variant="outline" size="sm" onClick={() => setHistoryOpen(true)}>
            <PanelLeft className="size-[var(--icon-size,1rem)]" aria-hidden="true" />
            Show conversations
          </Button>
        </div>
        {selectionFailed ? (
          <p role="alert" className="text-sm text-[hsl(var(--destructive))]">
            Could not load the selected conversation. Select another conversation and try again.
          </p>
        ) : null}
        {unsaved.length > 0 ? (
          <UnsavedBanner
            held={unsaved}
            onScreen={onScreen}
            onRetry={retryFailed}
            onDiscard={discardFailed}
          />
        ) : null}
        <ConversationHeader
          config={config}
          title={title}
          onTitleChange={retitle}
          onDelete={() => {
            const current = (conversations ?? []).find((c) => c.id === activeId)
            if (current) removeConversation(current)
          }}
          canDelete={activeId !== "" && !selectionPending}
          locked={locked}
          disabled={selectionPending}
          onOpenSettings={amendSettings}
        />

        <div className="flex min-h-0 flex-1 gap-4">
          <div className="flex min-h-0 min-w-0 flex-1 flex-col gap-4">
            {/* Centred and capped: a transcript run to the full width of a
                wide monitor is a line length nobody reads twice. */}
            <Card className="flex min-h-0 flex-1 flex-col overflow-hidden p-0">
              <div className="mx-auto flex min-h-0 w-full max-w-3xl flex-1 flex-col">
                <Transcript
                  messages={run.messages}
                  epoch={run.epoch}
                  routes={run.routes}
                  thinking={run.thinking}
                  busy={run.busy}
                  model={config.model}
                  seedNote={seedNote}
                  quiet
                  onChooseModel={amendSettings}
                />
              </div>

              {/* Inside the transcript's island rather than a card of its own
                  below it. Typing and reading the answer are one activity, and
                  the composer as a separate card put a gap and a second panel
                  edge between the two halves of it.

                  No rule above it. The field draws its own border, and a
                  divider a few pixels over that one is a second line saying
                  what the first already said. Capped to the transcript's
                  measure so the box lines up with the text it produces. */}
              <div className="shrink-0">
                <div className="mx-auto w-full max-w-3xl px-6 py-4">
                  <Composer
                    model={config.model}
                    busy={run.busy}
                    error={run.error}
                    problem={requestProblem(config)}
                    disabled={selectionPending}
                    onSend={(p) => void run.send(p)}
                    onStop={run.stop}
                  />
                </div>
              </div>
            </Card>
          </div>

          {/* The readings and the settings share the right-hand column: what
              this request is, and what it has cost. */}
          <div className="hidden w-80 shrink-0 flex-col gap-4 overflow-y-auto lg:flex">
            <TokenPanel
              consumption={consumptionOf(
                run.routes,
                run.messages.filter((m) => m.role === "assistant").length,
              )}
              metrics={metrics}
            />
            {/* A card, like the readings above it. The pane used to draw
                itself as a bare column with a hairline down its left edge,
                which put two different kinds of object in one stack and made
                the lower one read as scenery the layout had left behind. */}
            <Card className="flex shrink-0 flex-col gap-4 p-4">
              <ConfigPane
                config={config}
                // Edits until the first message and reads after it, the same
                // seam the dialog observes. Both write the one config, so
                // neither can be a keystroke behind the other.
                onChange={applySettings}
                locked={locked}
                // The model island above owns both, so the pane showing them
                // again would be two readings of one value.
                showModel={false}
                showDialect={false}
              />
            </Card>
          </div>
        </div>
      </ResizablePanel>

      <NewConversationDialog
        open={settingsOpen}
        onOpenChange={setSettingsOpen}
        seed={settingsSeed}
        onStart={applySettings}
        amending={settingsAmending}
      />
    </ResizablePanelGroup>
      <Sheet open={historyOpen} onOpenChange={setHistoryOpen}>
        <SheetContent side="left" className="flex w-full max-w-sm flex-col gap-0 p-4">
          <SheetHeader>
            <SheetTitle>Conversations</SheetTitle>
          </SheetHeader>
          <div className="mt-4 flex min-h-0 flex-1">
            <HistoryRail
              conversations={conversations ?? []}
              activeId={activeId}
              idPrefix="mobile-conversation"
              onSelect={(id) => {
                select(id)
                setHistoryOpen(false)
              }}
              onNew={() => {
                startNew()
                setHistoryOpen(false)
              }}
              onDelete={removeConversation}
            />
          </div>
        </SheetContent>
      </Sheet>
    </>
  )
}

function UnsavedBanner({
  held,
  onScreen,
  onRetry,
  onDiscard,
}: {
  held: HeldExchange[]
  onScreen: (id: string, selection: number) => boolean
  onRetry: () => void
  onDiscard: () => void
}) {
  const count = held.length
  const elsewhere = held.filter((h) => !onScreen(h.id, h.selection)).length
  const here = held.filter((h) => h.failed && onScreen(h.id, h.selection))
  const discardable = here.filter((h) => !h.questionStored).length
  const answerOnly = here.length - discardable

  const sentences = [
    count === 1
      ? "1 exchange was not saved. Newer exchanges in its conversation wait for it, so the stored conversation keeps the order they were sent in."
      : `${count} exchanges were not saved. Newer exchanges in the same conversation wait for them, so the stored conversation keeps the order they were sent in.`,
  ]
  if (elsewhere > 0) {
    sentences.push(
      count === 1
        ? "It is in another conversation."
        : elsewhere === 1
          ? "1 of them is in another conversation."
          : `${elsewhere} of them are in other conversations.`,
    )
  }
  if (discardable > 0) {
    sentences.push(
      "Discard drops this conversation's failed exchanges that have nothing stored yet: they stay on screen but out of the stored conversation.",
    )
  }
  if (answerOnly > 0) {
    sentences.push(
      answerOnly === 1
        ? "One exchange here cannot be discarded because its question is already stored: retrying saves its answer, and deleting the conversation removes both."
        : `${answerOnly} exchanges here cannot be discarded because their question is already stored: retrying saves their answers, and deleting the conversation removes them.`,
    )
  }

  return (
    <div role="alert" className="flex flex-wrap items-center gap-2">
      <p className="text-sm text-[hsl(var(--destructive))]">{sentences.join(" ")}</p>
      <Button variant="outline" size="sm" onClick={onRetry}>
        Retry saving
      </Button>
      {discardable > 0 ? (
        <Button variant="outline" size="sm" onClick={onDiscard}>
          Discard
        </Button>
      ) : null}
    </div>
  )
}
