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
import { parseTools, seedFromTrace } from "../lib/request"
import { ConfigPane } from "../config-pane/config-pane"
import { NO_METRICS, type StreamMetrics } from "../metrics"
import { TokenPanel, consumptionOf } from "../token-panel"
import { Transcript } from "../transcript"
import { Composer } from "../composer"
import { HistoryRail } from "./history-rail"
import { ConversationHeader } from "./conversation-header"
import { NewConversationDialog } from "./new-conversation-dialog"
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
  configRef.current = config
  // A config commit can be scheduled by the header and fire a moment later, so
  // the title it carries is read when it fires rather than when it was queued:
  // a rename in between would otherwise be undone by the write that follows it.
  const titleRef = useRef(title)
  titleRef.current = title
  // The create is memoized on its own promise rather than on the id it
  // resolves to: two exchanges completing while the first create is still in
  // flight would both read an empty conversationRef and make two
  // conversations for one thread.
  const creating = useRef<Promise<PlaygroundConversation> | null>(null)
  // Changes whenever the operator chooses which conversation owns the
  // screen. A create may still finish after that choice; it should persist
  // the completed turn, but it must not move the screen back to the thread it
  // created.
  const selectionGeneration = useRef(0)

  async function persistTurn(turn: CompletedTurn, ownerId: string) {
    try {
      // Ownership is captured by the render that starts the request. Reading
      // conversationRef here would file a slow answer under whichever thread
      // the operator selected while it was still streaming.
      let id = ownerId
      if (id === "") {
        if (creating.current === null) {
          creating.current = create.mutateAsync({
            title: titleRef.current === UNTITLED ? titleFromPrompt(turn.prompt) : titleRef.current,
            config: configRef.current,
          })
        }
        const createGeneration = selectionGeneration.current
        const made = await creating.current
        id = made.id
        if (selectionGeneration.current === createGeneration) {
          conversationRef.current = id
          setActiveId(id)
          // Marked loaded at creation, so the read below does not fetch the row
          // that was just written and replace the live transcript with it.
          setLoadedId(id)
          setTitle(made.title)
        }
      }
      const user = await append.mutateAsync({
        id, role: "user", content: turn.prompt, requestId: "",
      })
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
              { seq: user.seq, role: "user", content: turn.prompt, request_id: "", created_at: at },
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
    } catch {
      // useApiMutation has already reported it through the toaster. Losing a
      // saved turn must not take the transcript on screen down with it.
      // Cleared so a failed create does not make every later send await the
      // same rejected promise.
      creating.current = null
    }
  }

  const run = useChatRun(config, setMetrics, (turn) => void persistTurn(turn, activeId))

  useEffect(() => {
    if (!active) run.stop()
    // `run` is recreated on every render; visibility is the event that ends
    // an in-flight request, and stop reads the current controller through its
    // ref.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [active])

  // What fixes the settings is a turn existing, not the send that made it:
  // a conversation reopened from the rail has turns and no send behind it,
  // and its settings are every bit as committed to.
  const locked = run.messages.length > 0

  // The trace drawer's "Open in playground" arrives as ?seed=. It carried
  // its model and dialect into Lab's request pane, which is this screen now.
  const search = useSearch({ strict: false })
  const seed = search.seed
  const trace = useTrace(seed ?? "", { enabled: seed !== undefined })

  // Applied once per seed, and never over a conversation: a seed sets up a
  // fresh request, and stomping the model of a thread the operator opened
  // from the rail would rewrite what its answers were produced under.
  useEffect(() => {
    if (!trace.data || seed === undefined || seededFrom === seed) return
    if (run.messages.length > 0) return
    setConfig((prev) => ({ ...prev, ...seedFromTrace(trace.data as RequestTrace) }))
    setSeededFrom(seed)
    // run is a fresh object each render; the transcript's length is what
    // matters, and it is read rather than depended on for the same reason
    // config is set functionally above.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [trace.data, seed, seededFrom])

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

  function startNew() {
    selectionGeneration.current += 1
    // Seeded from the conversation being left rather than from the defaults:
    // the model an operator has been working with is almost always the one
    // they want next, and every value it carries is on screen in the dialog
    // rather than inherited invisibly. Cancel takes none of it.
    setSettingsSeed(config)
    conversationRef.current = ""
    creating.current = null
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
                    toolsError={parseTools(config.toolsRaw).error}
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
